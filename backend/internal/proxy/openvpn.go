package proxy

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"separ/internal/config"
	"separ/internal/env"
)

// OpenVPN can't run inside Xray, so the tunnel is brought up as a tun
// device without touching the routing table, and Xray's "proxy" outbound
// becomes a freedom outbound bound to that device. Everything else
// (capture, Iran split, DNS, kill switch, health checks) stays the same.

const OVPNDev = "tun-separ"

var OpenVPNBin = "/usr/sbin/openvpn"

func openvpnOutbound(link string) (*XOutbound, string, error) {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" {
		return nil, "", errors.New("bad openvpn reference")
	}
	ob := &XOutbound{
		Protocol: "freedom",
		Settings: map[string]any{"domainStrategy": "UseIPv4"},
		StreamSettings: &StreamSettings{
			Sockopt: map[string]any{"interface": OVPNDev},
		},
		addr: u.Query().Get("remote"),
	}
	ob.port, _ = strconv.Atoi(u.Query().Get("port"))
	return ob, fragmentName(u), nil
}

func IsOpenVPN(s config.Server) bool {
	return s.Protocol == "openvpn" || strings.HasPrefix(s.Link, "openvpn://")
}

// ImportOpenVPN validates an .ovpn file and returns the profile plus the
// server entry that points to it.
func ImportOpenVPN(conf, name, user, pass string) (config.OpenVPNProfile, config.Server, error) {
	conf = strings.ReplaceAll(conf, "\r\n", "\n")
	var remote, port, proto string
	for _, line := range strings.Split(conf, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "remote":
			if remote == "" && len(f) >= 2 {
				remote = f[1]
				if len(f) >= 3 {
					port = f[2]
				}
				if len(f) >= 4 {
					proto = f[3]
				}
			}
		case "proto":
			if proto == "" && len(f) >= 2 {
				proto = f[1]
			}
		}
	}
	if remote == "" {
		return config.OpenVPNProfile{}, config.Server{}, errors.New("no 'remote' line in the .ovpn file")
	}
	if !strings.Contains(conf, "<ca>") && !strings.Contains(conf, "\nca ") {
		return config.OpenVPNProfile{}, config.Server{}, errors.New("the .ovpn file has no embedded <ca> certificate")
	}
	if port == "" {
		port = "1194"
	}
	h := sha1.Sum([]byte(conf))
	id := hex.EncodeToString(h[:6])
	if name == "" {
		name = "OpenVPN " + remote
	}
	q := url.Values{"remote": {remote}, "port": {port}}
	link := (&url.URL{Scheme: "openvpn", Host: id, RawQuery: q.Encode(), Fragment: name}).String()
	p, _ := strconv.Atoi(port)
	srv := config.Server{ID: id, Name: name, Link: link, Protocol: "openvpn", Address: remote, Port: p,
		Network: strings.TrimSuffix(strings.TrimSuffix(proto, "-client"), "4")}
	return config.OpenVPNProfile{ID: id, Config: conf, Username: user, Password: pass}, srv, nil
}

// sanitizeOVPN removes directives that would change routes, DNS or run
// scripts on the router.
func sanitizeOVPN(conf string) string {
	var out []string
	drop := map[string]bool{
		"redirect-gateway": true, "route": true, "route-ipv6": true, "dhcp-option": true,
		"up": true, "down": true, "script-security": true, "dev": true, "dev-type": true,
		"auth-user-pass": true, "route-up": true, "ipchange": true, "block-outside-dns": true,
		"daemon": true, "log": true, "log-append": true, "status": true,
	}
	for _, line := range strings.Split(conf, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) > 0 && drop[f[0]] {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

type ovpnProc struct {
	id     string
	cancel context.CancelFunc
	exited chan struct{}
	tail   *tailBuffer
}

// startOpenVPN brings the tunnel up (or keeps it if already up for id).
func (m *Manager) startOpenVPN(id string) error {
	m.mu.Lock()
	cur := m.ovpn
	m.mu.Unlock()
	if cur != nil && cur.id == id && !chanClosed(cur.exited) && tunReady() {
		return nil
	}
	m.stopOpenVPN()
	if env.Mock {
		m.mu.Lock()
		m.ovpn = &ovpnProc{id: id, exited: make(chan struct{})}
		m.mu.Unlock()
		return nil
	}
	if _, err := os.Stat(OpenVPNBin); err != nil {
		return errors.New("openvpn is not installed (package openvpn-mbedtls)")
	}
	var prof *config.OpenVPNProfile
	for _, p := range m.Store.Get().Proxy.OpenVPN {
		if p.ID == id {
			p := p
			prof = &p
		}
	}
	if prof == nil {
		return errors.New("openvpn profile missing")
	}
	os.MkdirAll(env.DataDir, 0o700)
	confPath := filepath.Join(env.DataDir, "openvpn.conf")
	if err := os.WriteFile(confPath, []byte(sanitizeOVPN(prof.Config)), 0o600); err != nil {
		return err
	}
	args := []string{"--config", confPath, "--dev", OVPNDev, "--dev-type", "tun",
		"--route-nopull", "--pull-filter", "ignore", "dhcp-option",
		"--script-security", "1", "--verb", "3", "--auth-nocache", "--mute-replay-warnings"}
	if prof.Username != "" {
		authPath := filepath.Join(env.DataDir, "openvpn.auth")
		os.WriteFile(authPath, []byte(prof.Username+"\n"+prof.Password+"\n"), 0o600)
		args = append(args, "--auth-user-pass", authPath)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, OpenVPNBin, args...)
	tail := &tailBuffer{max: 8192}
	cmd.Stdout, cmd.Stderr = tail, tail
	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	m.mu.Lock()
	m.ovpn = &ovpnProc{id: id, cancel: cancel, exited: exited, tail: tail}
	m.mu.Unlock()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if chanClosed(exited) {
			return fmt.Errorf("openvpn exited: %s", ovpnError(tail.String()))
		}
		if tunReady() {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	m.stopOpenVPN()
	return fmt.Errorf("openvpn did not connect: %s", ovpnError(tail.String()))
}

func ovpnError(log string) string {
	for _, l := range splitLines(log) {
		if strings.Contains(l, "AUTH_FAILED") {
			return "wrong username or password"
		}
	}
	return lastLine(log)
}

func (m *Manager) stopOpenVPN() {
	m.mu.Lock()
	p := m.ovpn
	m.ovpn = nil
	m.mu.Unlock()
	if p == nil || p.cancel == nil {
		return
	}
	p.cancel()
	select {
	case <-p.exited:
	case <-time.After(5 * time.Second):
	}
}

func (m *Manager) ovpnLogs() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ovpn == nil || m.ovpn.tail == nil {
		return ""
	}
	return m.ovpn.tail.String()
}

// tunReady reports whether the tun device exists and has an IPv4 address.
func tunReady() bool {
	if env.Mock {
		return true
	}
	ifc, err := net.InterfaceByName(OVPNDev)
	if err != nil || ifc.Flags&net.FlagUp == 0 {
		return false
	}
	addrs, _ := ifc.Addrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			return true
		}
	}
	return false
}

func chanClosed(c chan struct{}) bool {
	if c == nil {
		return true
	}
	select {
	case <-c:
		return true
	default:
		return false
	}
}
