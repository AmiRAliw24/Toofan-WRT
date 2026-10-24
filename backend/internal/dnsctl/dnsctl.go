// Package dnsctl points dnsmasq at the tunnel resolver (or back to normal).
package dnsctl

import (
	"errors"
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

// render must only use options OpenWrt's own dnsmasq config never sets
// with a value: a repeated single-value option (cache-size, log-async, …)
// makes dnsmasq refuse to start, and the whole network loses DNS. Flags
// such as no-resolv and log-queries may repeat; server= is a list.
func render(p Plan) string {
	var b strings.Builder
	b.WriteString("# managed by separ - do not edit\n")
	switch {
	case p.TunnelPort > 0:
		b.WriteString("no-resolv\n")
		fmt.Fprintf(&b, "server=127.0.0.1#%d\n", p.TunnelPort)
	case len(p.Servers) > 0:
		b.WriteString("no-resolv\n")
		for _, s := range p.Servers {
			fmt.Fprintf(&b, "server=%s\n", s)
		}
	}
	if p.LogQueries {
		// Read back by the site log (logread).
		b.WriteString("log-queries\n")
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
	old, readErr := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	// Never restart into a config dnsmasq rejects: check it against the
	// router's real configuration first.
	if err := checkConfig(); err != nil {
		if readErr == nil {
			os.WriteFile(path, old, 0o644)
		} else {
			os.Remove(path)
		}
		return fmt.Errorf("dnsmasq rejected the DNS settings, kept the previous ones: %v", err)
	}
	last = content
	_, err := env.Run(20*time.Second, "/etc/init.d/dnsmasq", "restart")
	if !running() {
		// Last resort: without our file dnsmasq is the stock one again.
		os.Remove(path)
		last = ""
		env.Run(20*time.Second, "/etc/init.d/dnsmasq", "restart")
		return errors.New("dnsmasq did not start with the DNS settings; they were removed so the network keeps working")
	}
	return err
}

// checkConfig runs dnsmasq's syntax check on each generated instance config
// (they include our drop-in through conf-dir).
func checkConfig() error {
	confs, _ := filepath.Glob("/var/etc/dnsmasq.conf.*")
	for _, c := range confs {
		if strings.HasSuffix(c, ".tmp") {
			continue
		}
		if out, err := env.Run(10*time.Second, "dnsmasq", "--test", "-C", c); err != nil {
			return errors.New(strings.TrimSpace(out + " " + err.Error()))
		}
	}
	return nil
}

// Running reports whether dnsmasq is up.
func Running() bool {
	if env.Mock {
		return true
	}
	return running()
}

// Recover restarts dnsmasq; if it still won't run, our drop-in goes.
func Recover() {
	env.Run(20*time.Second, "/etc/init.d/dnsmasq", "restart")
	if running() {
		return
	}
	os.Remove(filepath.Join(confDir(), fileName))
	last = ""
	env.Run(20*time.Second, "/etc/init.d/dnsmasq", "restart")
}

func running() bool {
	for i := 0; i < 10; i++ {
		if out, err := env.Run(3*time.Second, "pidof", "dnsmasq"); err == nil && strings.TrimSpace(out) != "" {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// Remove drops our file (on uninstall or stop).
func Remove() {
	if env.Mock {
		return
	}
	os.Remove(filepath.Join(confDir(), fileName))
	env.Run(20*time.Second, "/etc/init.d/dnsmasq", "restart")
}
