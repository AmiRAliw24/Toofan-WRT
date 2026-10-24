// Package core wires the subsystems together and keeps the firewall and
// DNS in sync with the proxy state and per-device settings.
package core

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"separ/internal/config"
	"separ/internal/devices"
	"separ/internal/dnsctl"
	"separ/internal/env"
	"separ/internal/fw"
	"separ/internal/geo"
	"separ/internal/proxy"
	"separ/internal/system"
	"separ/internal/wifi"
)

type Core struct {
	Store   *config.Store
	Sampler *system.Sampler
	Devices *devices.Tracker
	Proxy   *proxy.Manager
	Quality *Quality

	applyMu   sync.Mutex
	pending   chan struct{}
	iranCIDRs []string
	lastErr   string
}

func New(store *config.Store, xrayBin string) *Core {
	c := &Core{
		Store:   store,
		Sampler: system.NewSampler(),
		Devices: devices.NewTracker(),
		pending: make(chan struct{}, 1),
	}
	c.Proxy = &proxy.Manager{
		Store:        store,
		XrayBin:      xrayBin,
		ISPDNS:       c.ispDNS,
		OnModeChange: c.RequestApply,
	}
	c.Proxy.Init()
	c.Quality = NewQuality(c)
	c.Devices.OnIPsChanged = c.RequestApply
	lastFP := proxyFingerprint(store.Get())
	var fpMu sync.Mutex
	store.OnChange(func() {
		c.RequestApply()
		fpMu.Lock()
		defer fpMu.Unlock()
		// Only proxy/DNS edits concern the engine; renaming a device must not
		// make the watchdog re-test servers.
		if fp := proxyFingerprint(store.Get()); fp != lastFP {
			lastFP = fp
			c.Proxy.Kick()
		}
	})
	devices.SetGuestIfaces(wifi.GuestIfnames)
	return c
}

func (c *Core) Start() {
	c.loadIran()
	go c.Sampler.Run()
	go c.Devices.Run()
	go c.applyLoop()
	go c.Proxy.Run()
	go c.Quality.Run()
	go c.guardLoop()
}

func (c *Core) Shutdown() {
	c.Proxy.Stop()
	fw.Remove()
	dnsctl.Remove()
}

func (c *Core) loadIran() {
	p := geo.FirstExisting(geo.GeoIPPaths)
	if p == "" {
		env.Logf("geoip.dat not found; Iran split runs inside Xray only")
		return
	}
	cidrs, err := geo.CountryCIDRs(p, "ir")
	if err != nil {
		env.Logf("geoip: %v", err)
		return
	}
	c.iranCIDRs = cidrs
	env.Logf("loaded %d Iranian networks", len(cidrs))
}

func (c *Core) ispDNS() []string {
	w := c.Sampler.WAN()
	if len(w.DNS) > 0 {
		return w.DNS
	}
	if env.Mock {
		return []string{"192.168.1.1"}
	}
	st, err := system.WANStatus()
	if err == nil {
		return st.DNS
	}
	return nil
}

// RequestApply schedules a (debounced) firewall + DNS refresh.
func (c *Core) RequestApply() {
	select {
	case c.pending <- struct{}{}:
	default:
	}
}

func (c *Core) applyLoop() {
	for range c.pending {
		time.Sleep(300 * time.Millisecond)
		c.apply()
	}
}

// guardLoop re-applies our table if something (a manual nft flush) removed it.
func (c *Core) guardLoop() {
	for {
		time.Sleep(30 * time.Second)
		if !fw.Exists() {
			env.Logf("nft table missing, restoring")
			c.RequestApply()
		}
	}
}

func lanIfaces(guest bool) (proxied, all []string) {
	if env.Mock {
		return []string{"br-lan"}, []string{"br-lan", "br-guest"}
	}
	dev := func(name string) string {
		out, err := env.Ubus("network.interface."+name, "status", "")
		if err != nil {
			return ""
		}
		var s struct {
			L3 string `json:"l3_device"`
		}
		json.Unmarshal([]byte(out), &s)
		return s.L3
	}
	lan := dev("lan")
	if lan == "" {
		lan = "br-lan"
	}
	proxied, all = []string{lan}, []string{lan}
	if g := dev(wifi.GuestNet); g != "" {
		all = append(all, g)
		if guest {
			proxied = append(proxied, g)
		}
	}
	return
}

func (c *Core) apply() {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	cfg := c.Store.Get()
	ips := c.Devices.IPs()

	proxied, all := lanIfaces(cfg.Proxy.GuestProxy)
	plan := fw.Plan{
		LANIfaces:  proxied,
		AllIfaces:  all,
		TProxy:     c.Proxy.Capturing(),
		TProxyPort: proxy.TProxyPort,
		BlockIPv6:  cfg.Proxy.Enabled && cfg.Proxy.BlockIPv6,
	}
	if cfg.Proxy.SplitIran {
		plan.IranCIDRs = c.iranCIDRs
	}
	for mac, d := range cfg.Devices {
		if d.Blocked {
			plan.BlockMACs = append(plan.BlockMACs, mac)
		}
		if d.Direct {
			plan.BypassMACs = append(plan.BypassMACs, mac)
		}
		if d.Priority && ips[mac] != "" {
			plan.PriorityIPs = append(plan.PriorityIPs, ips[mac])
		}
	}
	for mac, ip := range ips {
		d := cfg.Devices[mac]
		plan.Devices = append(plan.Devices, fw.DeviceRule{MAC: mac, IP: ip, DownKbps: d.DownKbps, UpKbps: d.UpKbps})
	}
	for _, s := range cfg.Schedules {
		if s.Enabled && len(s.MACs) > 0 {
			plan.Schedules = append(plan.Schedules, fw.ScheduleRule{MACs: lower(s.MACs), Days: s.Days, From: s.From, To: s.To})
		}
	}
	if err := fw.Apply(plan); err != nil {
		c.lastErr = err.Error()
		env.Logf("firewall: %v", err)
	} else {
		c.lastErr = ""
	}

	dp := dnsctl.Plan{LogQueries: cfg.DNS.LogSites}
	if c.Proxy.UseTunnelDNS() {
		dp.TunnelPort = proxy.DNSPort
	} else if p := proxy.FindProvider(cfg.DNS.Provider); p != nil && len(p.Servers) > 0 {
		dp.Servers = p.Servers
	} else if cfg.DNS.Provider == "custom" && cfg.DNS.Custom != "" && !strings.Contains(cfg.DNS.Custom, "://") {
		dp.Servers = strings.Fields(strings.ReplaceAll(cfg.DNS.Custom, ",", " "))
	}
	if err := dnsctl.Apply(dp); err != nil {
		env.Logf("dns: %v", err)
	}
}

func (c *Core) LastError() string { return c.lastErr }

func proxyFingerprint(cfg *config.Config) string {
	b, _ := json.Marshal([]any{cfg.Proxy, cfg.DNS})
	return string(b)
}

func lower(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = strings.ToLower(x)
	}
	return out
}
