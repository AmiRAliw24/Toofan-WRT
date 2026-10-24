// Package gaming covers what matters for online games: NAT type, UPnP,
// latency under load (bufferbloat) and smart queueing (SQM).
package gaming

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	mrand "math/rand/v2"
	"net"
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

var stunServers = []string{
	"stun.l.google.com:19302",
	"stun1.l.google.com:19302",
	"stun.cloudflare.com:3478",
	"stun.nextcloud.com:3478",
	"stun.sipgate.net:3478",
}

// DetectNAT sends STUN binding requests from one UDP socket to several
// servers: if they all see the same public port the mapping is
// endpoint-independent (good for games), otherwise it's symmetric.
func DetectNAT(wanIP string) NATResult {
	r := NATResult{Type: "unknown", WANIP: wanIP, TestedAt: time.Now().Unix()}
	r.DoubleNAT = isPrivate(net.ParseIP(wanIP))
	if env.Mock {
		r.Type, r.PublicIP, r.Mapping, r.Servers = "moderate", "5.120.33.71", "endpoint-independent", 2
		r.DoubleNAT = true
		return r
	}
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer conn.Close()
	var mapped []*net.UDPAddr
	seenIP := map[string]bool{}
	for _, srv := range stunServers {
		addr, err := net.ResolveUDPAddr("udp4", srv)
		if err != nil || seenIP[addr.IP.String()] {
			continue
		}
		m, err := stunBinding(conn, addr)
		if err != nil {
			continue
		}
		seenIP[addr.IP.String()] = true
		mapped = append(mapped, m)
		if len(mapped) == 3 {
			break
		}
	}
	r.Servers = len(mapped)
	if len(mapped) == 0 {
		r.Error = "no STUN server answered (UDP may be blocked)"
		return r
	}
	r.PublicIP = mapped[0].IP.String()
	if len(mapped) < 2 {
		r.Error = "only one STUN server answered"
		return r
	}
	same := true
	for _, m := range mapped[1:] {
		if m.Port != mapped[0].Port || !m.IP.Equal(mapped[0].IP) {
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

func stunBinding(conn *net.UDPConn, to *net.UDPAddr) (*net.UDPAddr, error) {
	req := make([]byte, 20)
	binary.BigEndian.PutUint16(req[0:], 0x0001) // Binding Request
	binary.BigEndian.PutUint32(req[4:], stunMagic)
	rand.Read(req[8:20])
	buf := make([]byte, 1500)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := conn.WriteToUDP(req, to); err != nil {
			return nil, err
		}
		conn.SetReadDeadline(time.Now().Add(time.Duration(1200+mrand.IntN(400)) * time.Millisecond))
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				break
			}
			if !from.IP.Equal(to.IP) || n < 20 || string(buf[8:20]) != string(req[8:20]) {
				continue
			}
			return parseStun(buf[:n])
		}
	}
	return nil, errors.New("timeout")
}

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
