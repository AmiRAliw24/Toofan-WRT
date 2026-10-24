package proxy

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"separ/internal/config"
	"separ/internal/env"
	"separ/internal/geo"
)

// Modes reported to the UI and used to decide firewall/DNS state.
const (
	ModeOff        = "off"
	ModeConnecting = "connecting"
	ModeConnected  = "connected"
	ModeDegraded   = "degraded"   // tunnel failing, watchdog is recovering
	ModeFallback   = "fallback"   // all servers down, traffic goes direct
	ModeKillSwitch = "killswitch" // all servers down, traffic blocked
	ModeSafe       = "safe"       // engine keeps crashing, direct internet
)

type Event struct {
	At   int64  `json:"at"`
	Kind string `json:"kind"` // info | warn | error
	Msg  string `json:"msg"`
}

type Status struct {
	Enabled   bool           `json:"enabled"`
	Mode      string         `json:"mode"`
	ActiveID  string         `json:"activeId"`
	Server    *config.Server `json:"server,omitempty"`
	LatencyMs int            `json:"latencyMs"`
	CheckedAt int64          `json:"checkedAt"`
	Fails     int            `json:"fails"`
	LastError string         `json:"lastError,omitempty"`
	Since     int64          `json:"since"`
	Events    []Event        `json:"events"`
	Testing   bool           `json:"testing"`
}

type Manager struct {
	Store        *config.Store
	XrayBin      string
	ISPDNS       func() []string
	OnModeChange func() // firewall + DNS follow the mode

	tester Tester

	mu        sync.Mutex
	mode      string
	activeID  string
	latency   int
	checkedAt time.Time
	fails     int
	lastErr   string
	since     time.Time
	events    []Event
	testing   bool
	crashes   []time.Time

	proc      *exec.Cmd
	cancel    context.CancelFunc
	tail      *tailBuffer
	exited    chan struct{}
	runningFP string

	lastPreferredTry time.Time
	lastAutoBest     time.Time
	lastSubUpdate    time.Time
	lastAllDownTry   time.Time
	kick             chan struct{}
}

func (m *Manager) Init() {
	m.tester.XrayBin = m.XrayBin
	m.mode = ModeOff
	m.kick = make(chan struct{}, 1)
}

func (m *Manager) Mode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mode
}

// Capturing reports whether LAN traffic should be sent into Xray.
func (m *Manager) Capturing() bool {
	switch m.Mode() {
	case ModeConnecting, ModeConnected, ModeDegraded, ModeKillSwitch:
		return true
	}
	return false
}

// UseTunnelDNS reports whether dnsmasq should forward to Xray's DNS port.
func (m *Manager) UseTunnelDNS() bool {
	switch m.Mode() {
	case ModeConnecting, ModeConnected, ModeDegraded:
		return true
	}
	return false
}

func (m *Manager) Status() Status {
	cfg := m.Store.Get()
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{
		Enabled: cfg.Proxy.Enabled, Mode: m.mode, ActiveID: m.activeID, LatencyMs: m.latency,
		Fails: m.fails, LastError: m.lastErr, Testing: m.testing,
		Events: append([]Event{}, m.events...),
	}
	if !m.checkedAt.IsZero() {
		st.CheckedAt = m.checkedAt.Unix()
	}
	if !m.since.IsZero() {
		st.Since = m.since.Unix()
	}
	for _, s := range cfg.Proxy.Servers {
		if s.ID == m.activeID {
			s := s
			st.Server = &s
		}
	}
	return st
}

func (m *Manager) logEvent(kind, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	env.Logf("proxy: %s", msg)
	m.events = append(m.events, Event{At: time.Now().Unix(), Kind: kind, Msg: msg})
	if len(m.events) > 60 {
		m.events = m.events[len(m.events)-60:]
	}
}

func (m *Manager) setMode(mode string) {
	m.mu.Lock()
	changed := m.mode != mode
	if changed {
		m.mode = mode
		m.since = time.Now()
	}
	m.mu.Unlock()
	if changed && m.OnModeChange != nil {
		m.OnModeChange()
	}
}

// Kick asks the watchdog to re-evaluate immediately (after config edits).
func (m *Manager) Kick() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Run is the watchdog loop.
func (m *Manager) Run() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	m.reconcile(true)
	for {
		select {
		case <-t.C:
			m.reconcile(false)
		case <-m.kick:
			m.reconcile(true)
		}
	}
}

func (m *Manager) reconcile(configChanged bool) {
	cfg := m.Store.Get().Proxy
	if !cfg.Enabled || cfg.Engine != "xray" {
		m.stopProc()
		m.mu.Lock()
		m.activeID, m.fails, m.lastErr = "", 0, ""
		m.mu.Unlock()
		m.setMode(ModeOff)
		return
	}
	if len(cfg.Servers) == 0 {
		m.stopProc()
		m.mu.Lock()
		m.lastErr = "no servers"
		m.mu.Unlock()
		m.setMode(m.downMode(cfg))
		return
	}

	m.maybeUpdateSubs(cfg)

	want := cfg.ActiveID
	if findServer(cfg.Servers, want) == nil {
		want = cfg.Servers[0].ID
		if p := findServer(cfg.Servers, cfg.PreferredID); p != nil {
			want = p.ID
		}
	}

	m.mu.Lock()
	running := m.procAlive()
	active := m.activeID
	mode := m.mode
	m.mu.Unlock()

	if mode == ModeSafe && !configChanged {
		if time.Since(m.since) < 5*time.Minute {
			return
		}
		m.mu.Lock()
		m.crashes = nil
		m.mu.Unlock()
	}

	if mode == ModeFallback || mode == ModeKillSwitch {
		// Everything was down: retry the whole list every minute.
		if !configChanged && time.Since(m.lastAllDownTry) < time.Minute {
			return
		}
		m.lastAllDownTry = time.Now()
		m.failover(cfg, "")
		return
	}

	if configChanged || !running || active != want {
		if !running && active != "" && !configChanged {
			if m.recordCrash() {
				m.stopProc()
				m.mu.Lock()
				m.logEvent("error", "engine crashed repeatedly, switching to safe mode")
				m.mu.Unlock()
				m.setMode(ModeSafe)
				return
			}
			m.mu.Lock()
			m.logEvent("warn", "engine stopped unexpectedly, restarting")
			m.mu.Unlock()
		}
		if mode == ModeOff || mode == ModeSafe {
			m.setMode(ModeConnecting)
		}
		if err := m.start(cfg, want, false); err != nil {
			m.mu.Lock()
			m.lastErr = err.Error()
			m.logEvent("error", "could not start %s: %v", serverName(cfg.Servers, want), err)
			m.mu.Unlock()
			m.failover(cfg, want)
			return
		}
	}

	m.healthCheck(cfg)
}

func (m *Manager) healthCheck(cfg config.Proxy) {
	d, err := m.probe()
	m.mu.Lock()
	m.checkedAt = time.Now()
	if err == nil {
		m.latency = int(d.Milliseconds())
		recovered := m.fails > 0
		m.fails, m.lastErr = 0, ""
		if recovered {
			m.logEvent("info", "tunnel recovered (%d ms)", m.latency)
		}
		active := m.activeID
		m.mu.Unlock()
		m.Store.UpdateQuiet(func(c *config.Config) {
			for i := range c.Proxy.Servers {
				if c.Proxy.Servers[i].ID == active {
					c.Proxy.Servers[i].LatencyMs = int(d.Milliseconds())
					c.Proxy.Servers[i].TestedAt = time.Now().Unix()
					c.Proxy.Servers[i].TestError = ""
				}
			}
		})
		m.setMode(ModeConnected)
		m.maybeReturnToPreferred(cfg)
		m.maybeAutoBest(cfg)
		return
	}
	m.fails++
	m.latency = -1
	m.lastErr = err.Error()
	fails, active := m.fails, m.activeID
	m.logEvent("warn", "health check failed (%d): %v", fails, err)
	m.mu.Unlock()
	m.setMode(ModeDegraded)

	if fails == 1 {
		// First failure: reconnect the same server.
		if err := m.start(cfg, active, true); err == nil {
			if d, err := m.probe(); err == nil {
				m.mu.Lock()
				m.fails, m.latency, m.lastErr = 0, int(d.Milliseconds()), ""
				m.logEvent("info", "reconnected to %s", serverName(cfg.Servers, active))
				m.mu.Unlock()
				m.setMode(ModeConnected)
			}
		}
		return
	}
	m.failover(cfg, active)
}

// failover tests every server and switches to the fastest one that answers.
func (m *Manager) failover(cfg config.Proxy, exclude string) {
	results := m.TestAll()
	best := ""
	bestMs := 1 << 30
	for _, r := range results {
		if r.LatencyMs > 0 && r.ID != exclude && r.LatencyMs < bestMs {
			best, bestMs = r.ID, r.LatencyMs
		}
	}
	if best == "" && exclude != "" {
		for _, r := range results {
			if r.ID == exclude && r.LatencyMs > 0 {
				best = r.ID
			}
		}
	}
	if best == "" {
		m.stopProc()
		m.mu.Lock()
		m.logEvent("error", "no server is reachable")
		m.lastErr = "no server is reachable"
		m.mu.Unlock()
		m.lastAllDownTry = time.Now()
		m.setMode(m.downMode(cfg))
		return
	}
	if err := m.start(cfg, best, false); err != nil {
		m.mu.Lock()
		m.lastErr = err.Error()
		m.mu.Unlock()
		m.setMode(m.downMode(cfg))
		return
	}
	m.Store.UpdateQuiet(func(c *config.Config) { c.Proxy.ActiveID = best })
	m.mu.Lock()
	m.fails, m.latency = 0, bestMs
	m.logEvent("info", "switched to %s (%d ms)", serverName(cfg.Servers, best), bestMs)
	m.mu.Unlock()
	m.setMode(ModeConnected)
}

func (m *Manager) maybeReturnToPreferred(cfg config.Proxy) {
	m.mu.Lock()
	active := m.activeID
	m.mu.Unlock()
	if cfg.PreferredID == "" || cfg.PreferredID == active || findServer(cfg.Servers, cfg.PreferredID) == nil {
		return
	}
	if time.Since(m.lastPreferredTry) < 3*time.Minute {
		return
	}
	m.lastPreferredTry = time.Now()
	r := m.TestServers([]string{cfg.PreferredID})
	if len(r) == 1 && r[0].LatencyMs > 0 {
		if err := m.start(cfg, cfg.PreferredID, false); err == nil {
			m.Store.UpdateQuiet(func(c *config.Config) { c.Proxy.ActiveID = cfg.PreferredID })
			m.mu.Lock()
			m.logEvent("info", "back on preferred server %s", serverName(cfg.Servers, cfg.PreferredID))
			m.mu.Unlock()
		}
	}
}

func (m *Manager) maybeAutoBest(cfg config.Proxy) {
	if !cfg.AutoBest || time.Since(m.lastAutoBest) < 15*time.Minute {
		return
	}
	m.lastAutoBest = time.Now()
	m.mu.Lock()
	active, cur := m.activeID, m.latency
	m.mu.Unlock()
	results := m.TestAll()
	for _, r := range results {
		if r.ID == active && r.LatencyMs > 0 {
			cur = r.LatencyMs
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].LatencyMs < results[j].LatencyMs })
	for _, r := range results {
		if r.LatencyMs <= 0 || r.ID == active {
			continue
		}
		// Only move for a clear win, not noise.
		if cur > 0 && float64(r.LatencyMs) < float64(cur)*0.7 && cur-r.LatencyMs > 60 {
			if err := m.start(cfg, r.ID, false); err == nil {
				m.Store.UpdateQuiet(func(c *config.Config) { c.Proxy.ActiveID = r.ID })
				m.mu.Lock()
				m.logEvent("info", "faster server %s (%d ms vs %d ms)", serverName(cfg.Servers, r.ID), r.LatencyMs, cur)
				m.mu.Unlock()
			}
		}
		break
	}
}

func (m *Manager) maybeUpdateSubs(cfg config.Proxy) {
	m.mu.Lock()
	due := time.Since(m.lastSubUpdate) >= 6*time.Hour
	m.mu.Unlock()
	if !cfg.SubAutoUpdate || len(cfg.Subscriptions) == 0 || !due {
		return
	}
	go m.UpdateSubscriptions("")
}

func (m *Manager) downMode(cfg config.Proxy) string {
	if cfg.KillSwitch {
		return ModeKillSwitch
	}
	return ModeFallback
}

func (m *Manager) recordCrash() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	keep := m.crashes[:0]
	for _, t := range m.crashes {
		if now.Sub(t) < 3*time.Minute {
			keep = append(keep, t)
		}
	}
	m.crashes = append(keep, now)
	return len(m.crashes) >= 4
}

// TestAll measures every configured server and stores the results.
func (m *Manager) TestAll() []TestResult {
	return m.TestServers(nil)
}

func (m *Manager) TestServers(ids []string) []TestResult {
	cfg := m.Store.Get().Proxy
	var list []config.Server
	for _, s := range cfg.Servers {
		if ids == nil || contains(ids, s.ID) {
			list = append(list, s)
		}
	}
	m.mu.Lock()
	m.testing = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.testing = false
		m.mu.Unlock()
	}()
	res := m.tester.Test(list, m.isp())
	byID := map[string]TestResult{}
	for _, r := range res {
		byID[r.ID] = r
	}
	now := time.Now().Unix()
	m.Store.UpdateQuiet(func(c *config.Config) {
		for i := range c.Proxy.Servers {
			if r, ok := byID[c.Proxy.Servers[i].ID]; ok {
				c.Proxy.Servers[i].LatencyMs = r.LatencyMs
				c.Proxy.Servers[i].TestError = r.Error
				c.Proxy.Servers[i].TestedAt = now
			}
		}
	})
	return res
}

func (m *Manager) isp() []string {
	if m.ISPDNS == nil {
		return nil
	}
	return m.ISPDNS()
}

func (m *Manager) probe() (time.Duration, error) {
	if env.Mock {
		if rand.Intn(40) == 0 {
			return 0, errors.New("timeout")
		}
		return time.Duration(110+rand.Intn(90)) * time.Millisecond, nil
	}
	return ProbeSocks(SocksPort, 10*time.Second)
}

// start (re)launches Xray for the given server.
func (m *Manager) start(cfg config.Proxy, id string, force bool) error {
	srv := findServer(cfg.Servers, id)
	if srv == nil {
		return errors.New("server not found")
	}
	full := m.Store.Get()
	opts := BuildOpts{
		Server:    *srv,
		SplitIran: cfg.SplitIran,
		BlockAds:  full.DNS.BlockAds,
		ISPDNS:    m.isp(),
		SecureDNS: SecureDNSFor(full.DNS),
		HaveIran:  geo.FirstExisting(geo.IranDatPaths) != "",
	}
	data, err := BuildConfig(opts)
	if err != nil {
		return err
	}
	fp := fmt.Sprintf("%x", sha1.Sum(data))
	m.mu.Lock()
	same := !force && m.procAlive() && m.activeID == id && m.runningFP == fp
	m.mu.Unlock()
	if same {
		return nil
	}
	m.stopProc()
	m.mu.Lock()
	m.runningFP = fp
	m.mu.Unlock()
	m.mu.Lock()
	m.activeID = id
	m.mu.Unlock()
	if env.Mock {
		m.mu.Lock()
		m.exited = make(chan struct{})
		m.mu.Unlock()
		return nil
	}
	os.MkdirAll(env.DataDir, 0o700)
	path := filepath.Join(env.DataDir, "xray.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	if out, err := env.Run(30*time.Second, m.XrayBin, "run", "-test", "-c", path); err != nil {
		return fmt.Errorf("config rejected: %s", lastLine(out+"\n"+err.Error()))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, m.XrayBin, "run", "-c", path)
	cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET=/usr/share/xray")
	tail := &tailBuffer{max: 16384}
	cmd.Stdout, cmd.Stderr = tail, tail
	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	m.mu.Lock()
	m.proc, m.cancel, m.tail, m.exited = cmd, cancel, tail, exited
	m.mu.Unlock()

	for i := 0; i < 50; i++ {
		select {
		case <-exited:
			return fmt.Errorf("engine exited: %s", lastLine(tail.String()))
		default:
		}
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", SocksPort), 100*time.Millisecond); err == nil {
			c.Close()
			if m.OnModeChange != nil {
				m.OnModeChange()
			}
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("engine did not open its ports")
}

func (m *Manager) procAlive() bool {
	if m.exited == nil {
		return false
	}
	select {
	case <-m.exited:
		return false
	default:
		return true
	}
}

func (m *Manager) stopProc() {
	m.mu.Lock()
	cancel, exited := m.cancel, m.exited
	m.proc, m.cancel, m.exited = nil, nil, nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		select {
		case <-exited:
		case <-time.After(3 * time.Second):
		}
	}
}

// Stop is called on shutdown.
func (m *Manager) Stop() { m.stopProc() }

// Logs returns the recent engine output.
func (m *Manager) Logs() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tail == nil {
		return ""
	}
	return m.tail.String()
}

func findServer(list []config.Server, id string) *config.Server {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

func serverName(list []config.Server, id string) string {
	if s := findServer(list, id); s != nil {
		return s.Name
	}
	return id
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
