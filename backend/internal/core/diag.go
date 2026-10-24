package core

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"sync"
	"time"

	"separ/internal/config"
	"separ/internal/env"
	"separ/internal/fw"
	"separ/internal/proxy"
)

// Step is one layer of the connection chain. The first failing step is the
// root cause; later failures are usually consequences of it.
type Step struct {
	ID     string `json:"id"`
	Status string `json:"status"` // ok | fail | skip
	Ms     int    `json:"ms,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type DiagReport struct {
	Steps     []Step `json:"steps"`
	RootCause string `json:"rootCause"` // id of the first failing step, "" when healthy
	At        int64  `json:"at"`
}

func timed(f func() (string, error)) (int, string, error) {
	start := time.Now()
	d, err := f()
	return int(time.Since(start).Milliseconds()), d, err
}

func resolveVia(server, name string) ([]string, error) {
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "udp", server)
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return r.LookupHost(ctx, name)
}

// Diagnose walks the chain from the WAN cable to DNS inside the tunnel.
func (c *Core) Diagnose() DiagReport {
	rep := DiagReport{At: time.Now().Unix()}
	if env.Mock {
		rep.Steps = []Step{
			{ID: "wan", Status: "ok", Detail: "dhcp 192.168.1.104"},
			{ID: "gateway", Status: "ok", Ms: 2, Detail: "192.168.1.1"},
			{ID: "isp-dns", Status: "ok", Ms: 31},
			{ID: "domestic", Status: "ok", Ms: 58},
			{ID: "foreign", Status: "ok", Ms: 140},
			{ID: "engine", Status: "ok"},
			{ID: "tunnel", Status: "ok", Ms: 171},
			{ID: "tunnel-dns", Status: "ok", Ms: 190},
			{ID: "iran-direct", Status: "ok", Ms: 64, Detail: "185.143.233.120"},
			{ID: "firewall", Status: "ok"},
		}
		return rep
	}
	add := func(s Step) { rep.Steps = append(rep.Steps, s) }
	w := c.Sampler.WAN()
	failed := false
	step := func(id string, skip bool, f func() (string, error)) {
		if skip {
			add(Step{ID: id, Status: "skip"})
			return
		}
		ms, d, err := timed(f)
		if err != nil {
			add(Step{ID: id, Status: "fail", Ms: ms, Detail: err.Error()})
			if !failed {
				rep.RootCause, failed = id, true
			}
			return
		}
		add(Step{ID: id, Status: "ok", Ms: ms, Detail: d})
	}

	step("wan", false, func() (string, error) {
		if !w.Up {
			return "", errString("WAN interface is down (cable or modem)")
		}
		return w.Proto + " " + w.IP, nil
	})
	step("gateway", w.Gateway == "", func() (string, error) {
		_, err := env.Run(6*time.Second, "ping", "-c", "2", "-W", "2", w.Gateway)
		if err != nil {
			return "", errString("modem does not answer ping")
		}
		return w.Gateway, nil
	})
	isp := c.ispDNS()
	step("isp-dns", len(isp) == 0, func() (string, error) {
		_, err := resolveVia(net.JoinHostPort(isp[0], "53"), "www.aparat.com")
		return isp[0], err
	})
	step("domestic", false, func() (string, error) {
		if bestConnect(domesticTargets) < 0 {
			return "", errString("no Iranian site reachable")
		}
		return "", nil
	})
	step("foreign", false, func() (string, error) {
		if bestConnect(foreignTargets) < 0 {
			return "", errString("international link unreachable directly")
		}
		return "", nil
	})
	// A filtered direct international route is normal in Iran; it must not
	// be blamed when the tunnel itself works.
	foreignFailed := rep.RootCause == "foreign"
	if foreignFailed {
		rep.RootCause, failed = "", false
	}

	cfg := c.Store.Get()
	mode := c.Proxy.Mode()
	off := !cfg.Proxy.Enabled
	step("engine", off, func() (string, error) {
		switch mode {
		case proxy.ModeSafe:
			return "", errString("engine keeps crashing (safe mode)")
		case proxy.ModeFallback, proxy.ModeKillSwitch:
			return "", errString("no server reachable")
		}
		return mode, nil
	})
	step("tunnel", off || mode == proxy.ModeSafe, func() (string, error) {
		d, err := proxy.ProbeSocks(proxy.SocksPort, 8*time.Second)
		if err != nil {
			return "", err
		}
		return d.String(), nil
	})
	step("tunnel-dns", off || !c.Proxy.UseTunnelDNS(), func() (string, error) {
		_, err := resolveVia(net.JoinHostPort("127.0.0.1", "5353"), "www.google.com")
		return "", err
	})
	// "Iranian sites direct": a typical Iranian site must resolve to an
	// Iranian address (so it skips the tunnel) and open from the line.
	step("iran-direct", off || cfg.Proxy.IranMode != config.IranDirect || !c.Proxy.UseTunnelDNS(), func() (string, error) {
		ips, err := resolveVia(net.JoinHostPort("127.0.0.1", "5353"), "www.digikala.com")
		if err != nil {
			return "", errString("Iranian names do not resolve: " + err.Error())
		}
		ip := ips[0]
		if len(c.iranCIDRs) > 0 && !inCIDRs(ip, c.iranCIDRs) {
			return "", errString(ip + " is not an Iranian address (DNS answered from abroad)")
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip, "443"), 5*time.Second)
		if err != nil {
			return "", errString(ip + " unreachable directly")
		}
		conn.Close()
		return ip, nil
	})
	step("firewall", false, func() (string, error) {
		if !fw.Exists() {
			return "", errString("panel firewall table missing")
		}
		if e := c.LastError(); e != "" {
			return "", errString(e)
		}
		return "", nil
	})
	if foreignFailed && rep.RootCause == "" && off {
		rep.RootCause = "foreign"
	}
	return rep
}

func inCIDRs(ip string, cidrs []string) bool {
	a := net.ParseIP(ip)
	if a == nil {
		return false
	}
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil && n.Contains(a) {
			return true
		}
	}
	return false
}

type errString string

func (e errString) Error() string { return string(e) }

// ---- incidents: outages kept for 60 days ----

type Incident struct {
	Kind  string `json:"kind"` // line | international | tunnel
	Start int64  `json:"start"`
	End   int64  `json:"end,omitempty"`
}

type Incidents struct {
	mu   sync.Mutex
	list []Incident
	open map[string]int // kind -> index of open incident
	path string
}

func NewIncidents(path string) *Incidents {
	in := &Incidents{path: path, open: map[string]int{}}
	if env.Mock {
		now := time.Now().Unix()
		in.list = []Incident{
			{Kind: "international", Start: now - 3*86400, End: now - 3*86400 + 420},
			{Kind: "line", Start: now - 9*86400, End: now - 9*86400 + 1800},
			{Kind: "tunnel", Start: now - 20*3600, End: now - 20*3600 + 95},
		}
		return in
	}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &in.list)
	}
	for i, x := range in.list {
		if x.End == 0 {
			in.list[i].End = x.Start // unknown end after a reboot
		}
	}
	return in
}

// Observe updates incident state from the current health of each kind.
func (in *Incidents) Observe(kind string, ok bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	now := time.Now().Unix()
	idx, isOpen := in.open[kind]
	switch {
	case !ok && !isOpen:
		in.list = append(in.list, Incident{Kind: kind, Start: now})
		in.open[kind] = len(in.list) - 1
	case ok && isOpen:
		in.list[idx].End = now
		delete(in.open, kind)
		in.saveLocked()
	}
}

func (in *Incidents) saveLocked() {
	cut := time.Now().Add(-60 * 24 * time.Hour).Unix()
	keep := in.list[:0]
	for _, x := range in.list {
		if x.Start >= cut {
			keep = append(keep, x)
		}
	}
	in.list = keep
	in.open = map[string]int{}
	for i, x := range in.list {
		if x.End == 0 {
			in.open[x.Kind] = i
		}
	}
	if env.Mock {
		return
	}
	b, _ := json.Marshal(in.list)
	os.WriteFile(in.path+".tmp", b, 0o600)
	os.Rename(in.path+".tmp", in.path)
}

func (in *Incidents) List() []Incident {
	in.mu.Lock()
	defer in.mu.Unlock()
	out := make([]Incident, len(in.list))
	for i := range in.list {
		out[len(in.list)-1-i] = in.list[i] // newest first
	}
	return out
}
