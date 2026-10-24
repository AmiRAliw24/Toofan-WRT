// Package core wires the subsystems together and keeps the firewall and
// DNS in sync with the proxy state and per-device settings.
package core

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"separ/internal/config"
	"separ/internal/devices"
	"separ/internal/dnsctl"
	"separ/internal/env"
	"separ/internal/fw"
	"separ/internal/gaming"
	"separ/internal/geo"
	"separ/internal/led"
	"separ/internal/luci"
	"separ/internal/netinfo"
	"separ/internal/proxy"
	"separ/internal/sitelog"
	"separ/internal/system"
	"separ/internal/wifi"
)

type Core struct {
	Store     *config.Store
	Sampler   *system.Sampler
	Devices   *devices.Tracker
	Proxy     *proxy.Manager
	Quality   *Quality
	Speed     SpeedTest
	Bloat     gaming.Bloat
	LED       *led.Controller
	Incidents *Incidents
	Sites     *sitelog.Log
	// PanelPorts are the ports the UI listens on (for the WAN lock).
	PanelPorts []int

	applyMu sync.Mutex
	sqmKey  string

	infoMu    sync.Mutex
	lineInfo  netinfo.Info
	exitInfo  netinfo.Info
	exitFor   string
	pending   chan struct{}
	meter     speedMeter
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
		KernelIran:   func() bool { return len(c.iranCIDRs) > 0 },
	}
	c.Proxy.Init()
	c.Quality = NewQuality(c)
	c.Incidents = NewIncidents("/etc/separ/incidents.json")
	c.LED = led.New()
	c.LED.Config = func() config.LED { return c.Store.Get().LED }
	c.LED.Status = c.ledStatus
	c.Devices.OnIPsChanged = c.RequestApply
	c.Sites = sitelog.New(c.Devices.MACForIP)
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
	go c.LED.Run()
	go c.nightly()
	go c.infoLoop()
	go c.speedLoop()
}

// infoLoop keeps the line's and the tunnel's public IP/ISP fresh.
func (c *Core) infoLoop() {
	lastWAN := ""
	for {
		w := c.Sampler.WAN()
		c.infoMu.Lock()
		line, exit, exitFor := c.lineInfo, c.exitInfo, c.exitFor
		c.infoMu.Unlock()
		now := time.Now().Unix()
		if w.Up && (w.IP != lastWAN || now-line.At > 900 || (line.Error != "" && now-line.At > 60)) {
			lastWAN = w.IP
			li := netinfo.Lookup(0)
			c.infoMu.Lock()
			c.lineInfo = li
			c.infoMu.Unlock()
		}
		st := c.Proxy.Status()
		if st.Mode == proxy.ModeConnected {
			if st.ActiveID != exitFor || now-exit.At > 600 || (exit.Error != "" && now-exit.At > 60) {
				ei := netinfo.Lookup(proxy.SocksPort)
				c.infoMu.Lock()
				c.exitInfo, c.exitFor = ei, st.ActiveID
				c.infoMu.Unlock()
			}
		} else if exitFor != "" {
			c.infoMu.Lock()
			c.exitInfo, c.exitFor = netinfo.Info{}, ""
			c.infoMu.Unlock()
		}
		time.Sleep(20 * time.Second)
	}
}

// PublicInfo returns the line's ISP/IP and the tunnel exit (if connected).
func (c *Core) PublicInfo() (line, exit netinfo.Info) {
	c.infoMu.Lock()
	defer c.infoMu.Unlock()
	return c.lineInfo, c.exitInfo
}

func (c *Core) ledStatus() led.Status {
	w := c.Sampler.WAN()
	sp := c.Speed.Get()
	grade := "bad"
	switch {
	case sp.DownMbps >= 20:
		grade = "ok"
	case sp.DownMbps >= 5:
		grade = "mid"
	}
	q := c.Quality.Last()
	mbps, _ := c.SpeedEstimate()
	return led.Status{
		SpeedMbps: mbps,
		WANUp:     w.Up, ProxyMode: c.Proxy.Mode(), Split: c.Store.Get().Proxy.SplitIran,
		ExtPingMs: q.Ext, SpeedTest: sp.Running, SpeedGrade: grade,
	}
}

// nightly runs the 04:00 maintenance: smart Wi-Fi channel selection.
func (c *Core) nightly() {
	last := ""
	for {
		time.Sleep(time.Minute)
		now := time.Now()
		day := now.Format("2006-01-02")
		if now.Hour() != 4 || last == day {
			continue
		}
		last = day
		if c.Store.Get().SmartWiFi {
			for _, band := range []string{"2g", "5g"} {
				if _, err := wifi.Optimize(band, true); err != nil {
					env.Logf("smart wifi %s: %v", band, err)
				}
			}
		}
	}
}

// applySQM (re)installs the shaper when settings or the WAN device change.
func (c *Core) applySQM(cfg *config.Config) {
	wan := c.Sampler.WAN().Device
	down, up := 0, 0
	if cfg.SQM.Enabled {
		down, up = cfg.SQM.DownMbps*1000, cfg.SQM.UpMbps*1000
	}
	key := strings.Join([]string{wan, strconv.Itoa(down), strconv.Itoa(up)}, "/")
	if key == c.sqmKey || wan == "" {
		return
	}
	c.sqmKey = key
	if err := gaming.ApplySQM(wan, down, up); err != nil {
		env.Logf("sqm: %v", err)
		c.sqmKey = ""
	}
}

// RestartMarker exists while the updater restarts the panel; the firewall
// and DNS are then left in place so traffic can't leak around the tunnel.
const RestartMarker = "/tmp/separ-restarting"

func (c *Core) Shutdown() {
	c.Proxy.Stop()
	c.Devices.SaveUsage()
	if _, err := os.Stat(RestartMarker); err == nil {
		os.Remove(RestartMarker)
		return
	}
	gaming.RemoveSQM(c.Sampler.WAN().Device)
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

// Reapply forces a full refresh, including the shaper (after WAN restarts).
func (c *Core) Reapply() {
	c.applyMu.Lock()
	c.sqmKey = ""
	c.applyMu.Unlock()
	c.RequestApply()
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

// guardLoop re-applies our table if something (a manual nft flush) removed
// it, and follows Wi-Fi interface renames after Wi-Fi reloads.
func (c *Core) guardLoop() {
	last := ""
	for {
		time.Sleep(30 * time.Second)
		if !fw.Exists() {
			env.Logf("nft table missing, restoring")
			c.RequestApply()
		}
		// Without dnsmasq the whole network loses DNS.
		if !dnsctl.Running() {
			env.Logf("dnsmasq is not running, recovering")
			dnsctl.Recover()
		}
		if k := strings.Join(c.directNetdevs(c.Store.Get()), ","); k != last {
			last = k
			c.RequestApply()
		}
	}
}

// directNetdevs resolves "direct" Wi-Fi networks to their current devices.
func (c *Core) directNetdevs(cfg *config.Config) []string {
	if len(cfg.WiFiRoutes) == 0 {
		return nil
	}
	devs := wifi.Netdevs()
	var out []string
	for sec, route := range cfg.WiFiRoutes {
		if route == "direct" && devs[sec] != "" {
			out = append(out, devs[sec])
		}
	}
	sort.Strings(out)
	return out
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
		LockWAN:    cfg.LockWAN,
		WANDev:     c.Sampler.WAN().Device,
		PanelPorts: append(append([]int{}, c.PanelPorts...), luci.AltPort),
	}
	switch {
	case cfg.Proxy.IranMode == config.IranDirect:
		plan.IranCIDRs = c.iranCIDRs
	case cfg.Proxy.IranMode == config.IranBlock && cfg.Proxy.Enabled:
		plan.IranCIDRs = c.iranCIDRs
		plan.IranBlock = len(c.iranCIDRs) > 0
	}
	plan.DirectNetdevs = c.directNetdevs(cfg)
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
	c.Sites.SetEnabled(cfg.DNS.LogSites)
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
	c.applySQM(cfg)
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
