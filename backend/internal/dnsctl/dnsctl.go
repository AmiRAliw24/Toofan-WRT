// Package dnsctl points dnsmasq at the tunnel resolver (or back to normal).
package dnsctl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"separ/internal/env"
)

const fileName = "separ.conf"

type Plan struct {
	TunnelPort int      // >0: forward everything to Xray's DNS inbound
	Servers    []string // plain upstreams when not using the tunnel (empty = ISP)
	LogQueries bool
}

func render(p Plan) string {
	var b strings.Builder
	b.WriteString("# managed by separ - do not edit\n")
	switch {
	case p.TunnelPort > 0:
		b.WriteString("no-resolv\n")
		fmt.Fprintf(&b, "server=127.0.0.1#%d\n", p.TunnelPort)
		// Xray answers from its own cache; keep dnsmasq's small to follow changes.
		b.WriteString("cache-size=1000\n")
	case len(p.Servers) > 0:
		b.WriteString("no-resolv\n")
		for _, s := range p.Servers {
			fmt.Fprintf(&b, "server=%s\n", s)
		}
	}
	if p.LogQueries {
		b.WriteString("log-queries=extra\n")
	}
	return b.String()
}

// confDir finds the directory dnsmasq includes. OpenWrt 23.05+ uses one
// per instance (/tmp/dnsmasq.<section>.d), older releases /tmp/dnsmasq.d.
func confDir() string {
	if out, err := env.Run(3*time.Second, "uci", "-q", "get", "dhcp.@dnsmasq[0].confdir"); err == nil && out != "" {
		return out
	}
	if m, _ := filepath.Glob("/tmp/dnsmasq.*.d"); len(m) > 0 {
		return m[0]
	}
	return "/tmp/dnsmasq.d"
}

var last string

// Apply writes the drop-in and restarts dnsmasq only when it changed.
func Apply(p Plan) error {
	content := render(p)
	if env.Mock || content == last {
		last = content
		return nil
	}
	dir := confDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, fileName)
	if old, err := os.ReadFile(path); err == nil && string(old) == content {
		last = content
		return nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	last = content
	_, err := env.Run(20*time.Second, "/etc/init.d/dnsmasq", "restart")
	return err
}

// Remove drops our file (on uninstall or stop).
func Remove() {
	if env.Mock {
		return
	}
	os.Remove(filepath.Join(confDir(), fileName))
	env.Run(20*time.Second, "/etc/init.d/dnsmasq", "restart")
}
