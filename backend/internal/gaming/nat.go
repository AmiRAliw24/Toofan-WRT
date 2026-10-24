// Package gaming covers what matters for online games: NAT type, UPnP,
// latency under load (bufferbloat) and smart queueing (SQM).
package gaming

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"time"

	"separ/internal/env"
)

type NATResult struct {
	Type      string `json:"type"` // open | moderate | strict | unknown
	PublicIP  string `json:"publicIp"`
	WANIP     string `json:"wanIp"`
	DoubleNAT bool   `json:"doubleNat"` // the WAN address is private: ISP or modem NAT in front
	Mapping   string `json:"mapping"`   // endpoint-independent | endpoint-dependent
	Servers   int    `json:"servers"`   // STUN servers that answered
	TestedAt  int64  `json:"testedAt"`
	Error     string `json:"error,omitempty"`
}

// A broad list: some are filtered in Iran, so many are asked at once.
var stunServers = []string{
	"stun.l.google.com:19302",
	"stun1.l.google.com:19302",
	"stun.cloudflare.com:3478",
	"global.stun.twilio.com:3478",
	"stun.miwifi.com:3478",
	"stun.qq.com:3478",
	"stun.chat.bilibili.com:3478",
	"stun.nextcloud.com:3478",
	"stun.sipgate.net:3478",
	"stun.ekiga.net:3478",
	"stun.voipgate.com:3478",
	"stun.stunprotocol.org:3478",
}

// DetectNAT sends STUN binding requests from one UDP socket to several
// servers at once: if they all see the same public port the mapping is
// endpoint-independent (good for games), otherwise it's symmetric.
func DetectNAT(wanIP string) NATResult {
	r := NATResult{Type: "unknown", WANIP: wanIP, TestedAt: time.Now().Unix()}
	r.DoubleNAT = isPrivate(net.ParseIP(wanIP))
	if env.Mock {
		r.Type, r.PublicIP, r.Mapping, r.Servers = "moderate", "5.120.33.71", "endpoint-independent", 3
		r.DoubleNAT = true
		return r
	}
	// Resolve in parallel; DNS failures are reported separately from UDP ones.
	type target struct {
		addr *net.UDPAddr
		txid string
	}
	var mu sync.Mutex
	var targets []target
	seen := map[string]bool{}
	resolved := 0
	var wg sync.WaitGroup
	for _, srv := range stunServers {
		wg.Add(1)
		go func(srv string) {
			defer wg.Done()
			addr, err := net.ResolveUDPAddr("udp4", srv)
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			resolved++
			if !seen[addr.IP.String()] {
				seen[addr.IP.String()] = true
				targets = append(targets, target{addr: addr})
			}
		}(srv)
	}
	wg.Wait()
	if resolved == 0 {
		r.Error = "could not resolve any STUN server (DNS problem)"
		return r
	}
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer conn.Close()
	reqs := make([][]byte, len(targets))
	for i := range targets {
		reqs[i] = make([]byte, 20)
		binary.BigEndian.PutUint16(reqs[i][0:], 0x0001) // Binding Request
		binary.BigEndian.PutUint32(reqs[i][4:], stunMagic)
		rand.Read(reqs[i][8:20])
		targets[i].txid = string(reqs[i][8:20])
	}
	send := func() {
		for i, t := range targets {
			conn.WriteToUDP(reqs[i], t.addr)
		}
	}
	send()
	mapped := map[string]*net.UDPAddr{} // by server IP
	deadline := time.Now().Add(3500 * time.Millisecond)
	resent := false
	buf := make([]byte, 1500)
	for time.Now().Before(deadline) && len(mapped) < len(targets) {
		if !resent && time.Until(deadline) < 2*time.Second {
			resent = true
			send()
		}
		conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil || n < 20 {
			continue
		}
		for _, t := range targets {
			if t.addr.IP.Equal(from.IP) && string(buf[8:20]) == t.txid {
				if m, err := parseStun(buf[:n]); err == nil {
					mapped[from.IP.String()] = m
				}
			}
		}
	}
	r.Servers = len(mapped)
	if len(mapped) == 0 {
		r.Error = "no STUN server answered: outgoing UDP seems blocked by the ISP or modem"
		return r
	}
	var list []*net.UDPAddr
	for _, m := range mapped {
		list = append(list, m)
	}
	r.PublicIP = list[0].IP.String()
	if len(list) < 2 {
		r.Error = "only one STUN server answered"
		return r
	}
	same := true
	for _, m := range list[1:] {
		if m.Port != list[0].Port || !m.IP.Equal(list[0].IP) {
			same = false
		}
	}
	switch {
	case same && r.PublicIP == wanIP:
		r.Type, r.Mapping = "open", "endpoint-independent"
	case same:
		r.Type, r.Mapping = "moderate", "endpoint-independent"
	default:
		r.Type, r.Mapping = "strict", "endpoint-dependent"
	}
	return r
}

func isPrivate(ip net.IP) bool {
	if ip == nil {
		return false
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	return ip.IsPrivate() || cgnat.Contains(ip)
}

const stunMagic = 0x2112A442

func parseStun(b []byte) (*net.UDPAddr, error) {
	if binary.BigEndian.Uint16(b[0:]) != 0x0101 {
		return nil, errors.New("not a binding success")
	}
	attrs := b[20:]
	var fallback *net.UDPAddr
	for len(attrs) >= 4 {
		t := binary.BigEndian.Uint16(attrs[0:])
		l := int(binary.BigEndian.Uint16(attrs[2:]))
		if len(attrs) < 4+l {
			break
		}
		v := attrs[4 : 4+l]
		if l >= 8 && v[1] == 0x01 {
			port := binary.BigEndian.Uint16(v[2:])
			ip := net.IP(append([]byte{}, v[4:8]...))
			switch t {
			case 0x0020: // XOR-MAPPED-ADDRESS
				port ^= stunMagic >> 16
				var magic [4]byte
				binary.BigEndian.PutUint32(magic[:], stunMagic)
				for i := range ip {
					ip[i] ^= magic[i]
				}
				return &net.UDPAddr{IP: ip, Port: int(port)}, nil
			case 0x0001: // MAPPED-ADDRESS
				fallback = &net.UDPAddr{IP: ip, Port: int(port)}
			}
		}
		attrs = attrs[4+(l+3)&^3:]
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, errors.New("no mapped address")
}
