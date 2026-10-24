package devices

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"toofanwrt/internal/env"
	"toofanwrt/internal/uci"
)

var (
	mockStaticMu sync.Mutex
	mockStatic   = map[string]string{}
)

// hostSection finds the dhcp "host" section reserving an address for mac
// (ours or one made elsewhere) and its IP.
func hostSection(mac string) (section, ip string) {
	all, err := uci.Show("dhcp")
	if err != nil {
		return "", ""
	}
	for k, v := range all {
		if !strings.HasSuffix(k, ".mac") {
			continue
		}
		for _, m := range strings.Fields(v) {
			if strings.EqualFold(m, mac) {
				sec := strings.TrimSuffix(k, ".mac")
				return sec, all[sec+".ip"]
			}
		}
	}
	return "", ""
}

// StaticIP returns the address reserved for a device, "" when it has none.
func StaticIP(mac string) string {
	if env.Mock {
		mockStaticMu.Lock()
		defer mockStaticMu.Unlock()
		return mockStatic[mac]
	}
	_, ip := hostSection(mac)
	return ip
}

// lanNet is the router's LAN address and subnet.
func lanNet() (net.IP, *net.IPNet) {
	if env.Mock {
		_, n, _ := net.ParseCIDR("192.168.1.0/24")
		return net.ParseIP("192.168.1.1"), n
	}
	cfg, _ := uci.Show("network")
	addr := cfg["network.lan.ipaddr"]
	if strings.Contains(addr, "/") {
		ip, n, err := net.ParseCIDR(strings.Fields(addr)[0])
		if err == nil {
			return ip, n
		}
	}
	ip := net.ParseIP(addr)
	mask := net.IPMask(net.ParseIP(cfg["network.lan.netmask"]).To4())
	if ip == nil || mask == nil {
		return nil, nil
	}
	return ip, &net.IPNet{IP: ip.Mask(mask), Mask: mask}
}

// SetStaticIP reserves ip for mac (ip "" removes the reservation). The
// device gets it on its next DHCP renewal.
func SetStaticIP(mac, ip, name string) error {
	if ip != "" {
		a := net.ParseIP(ip).To4()
		router, lan := lanNet()
		if a == nil {
			return errors.New("invalid IP address")
		}
		if lan != nil && (!lan.Contains(a) || a.Equal(router) || a.Equal(lan.IP) || a[3] == 255) {
			return errors.New("the address must be inside the home network (" + lan.String() + ") and not the router's")
		}
	}
	if env.Mock {
		mockStaticMu.Lock()
		defer mockStaticMu.Unlock()
		if ip == "" {
			delete(mockStatic, mac)
		} else {
			mockStatic[mac] = ip
		}
		return nil
	}
	// Another device must not hold the same reservation.
	if ip != "" {
		all, _ := uci.Show("dhcp")
		for k, v := range all {
			if strings.HasSuffix(k, ".ip") && v == ip {
				sec := strings.TrimSuffix(k, ".ip")
				if !strings.EqualFold(strings.Fields(all[sec+".mac"] + " ")[0], mac) {
					return errors.New("this address is already reserved for another device")
				}
			}
		}
	}
	sec, _ := hostSection(mac)
	b := uci.NewBatch()
	switch {
	case ip == "" && sec == "":
		return nil
	case ip == "":
		b.Delete(sec)
	default:
		if sec == "" {
			sec = "dhcp.separ_" + strings.ReplaceAll(mac, ":", "")
			b.Set(sec, "host")
			b.Set(sec+".mac", mac)
		}
		b.Set(sec+".ip", ip)
		if name = sanitizeHostname(name); name != "" {
			b.Set(sec+".name", name)
		}
	}
	if err := b.Commit(); err != nil {
		return err
	}
	_, err := env.Run(20*time.Second, "/etc/init.d/dnsmasq", "reload")
	return err
}

func sanitizeHostname(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// Wake sends a Wake-on-LAN magic packet on the home network.
func Wake(mac string) error {
	hw, err := hex.DecodeString(strings.NewReplacer(":", "", "-", "").Replace(mac))
	if err != nil || len(hw) != 6 {
		return errors.New("invalid MAC")
	}
	pkt := make([]byte, 0, 102)
	for i := 0; i < 6; i++ {
		pkt = append(pkt, 0xff)
	}
	for i := 0; i < 16; i++ {
		pkt = append(pkt, hw...)
	}
	if env.Mock {
		return nil
	}
	targets := []string{"255.255.255.255:9"}
	if _, lan := lanNet(); lan != nil {
		bc := make(net.IP, 4)
		for i := range bc {
			bc[i] = lan.IP.To4()[i] | ^lan.Mask[i]
		}
		targets = append(targets, bc.String()+":9")
	}
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return err
	}
	defer conn.Close()
	var lastErr error
	sent := false
	for _, t := range targets {
		addr, _ := net.ResolveUDPAddr("udp4", t)
		if _, err := conn.WriteTo(pkt, addr); err != nil {
			lastErr = err
		} else {
			sent = true
		}
	}
	if !sent {
		return lastErr
	}
	return nil
}

// MACForIP returns the device currently using ip.
func (t *Tracker) MACForIP(ip string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for mac, d := range t.devs {
		if d.IP == ip {
			return mac
		}
	}
	return ""
}

// Connections counts the device's open connections in the conntrack table.
func Connections(ip string) int {
	if ip == "" {
		return 0
	}
	if env.Mock {
		return 20 + int(time.Now().Unix()%37)
	}
	f, err := os.Open("/proc/net/nf_conntrack")
	if err != nil {
		return -1
	}
	defer f.Close()
	needle := []byte("src=" + ip + " ")
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), 4096)
	for sc.Scan() {
		// Only the original direction: the first src= on the line.
		line := sc.Bytes()
		if i := bytes.Index(line, []byte("src=")); i >= 0 && bytes.HasPrefix(line[i:], needle) {
			n++
		}
	}
	return n
}
