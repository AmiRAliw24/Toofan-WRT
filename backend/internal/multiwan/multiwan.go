// Package multiwan aggregates the speed of several internet links: the WAN
// cable and up to two Wi-Fi uplinks (one per radio).
//
// It is per-connection load balancing, the standard way (mwan3 works the
// same): each link gets its own routing table and an ip rule on its mark;
// nftables (fw.renderBalance) gives every new connection a link by weight
// and keeps the whole connection on it. Many connections at once (several
// devices, downloads, the tunnel's parallel connections) add the links up; a
// single connection still runs on one link.
//
// Every link is probed through its own interface every 10 s. A link that
// stops answering is taken out (its open connections end, new ones go to
// the others) and comes back once it answers again.
package multiwan

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"toofanwrt/internal/env"
	"toofanwrt/internal/fw"
)

// Candidate is a netifd interface that can carry internet.
type Candidate struct {
	Name string // netifd interface
	Kind string // cable | wifi
	Slot int    // 0 = cable, 1-2 = Wi-Fi uplink slot
}

var Candidates = []Candidate{{"wan", "cable", 0}, {"wwan", "wifi", 1}, {"wwan2", "wifi", 2}}

func markOf(slot int) uint32 { return uint32(slot+1) << 16 }
func tableOf(slot int) int   { return 201 + slot }
func prefOf(slot int) int    { return 1201 + slot }

// Probe targets: answered from inside Iran and abroad alike. A link is
// healthy when any of them answers.
var Targets = []string{"8.8.8.8", "1.1.1.1", "4.2.2.4", "9.9.9.9"}

type Link struct {
	Name    string  `json:"name"`
	Kind    string  `json:"kind"`
	Slot    int     `json:"slot"`
	Device  string  `json:"device"`
	Up      bool    `json:"up"`      // netifd: up with an address
	Healthy bool    `json:"healthy"` // reaches the internet
	IP      string  `json:"ip"`
	Gateway string  `json:"gateway"`
	RTT     int     `json:"rttMs"`
	Weight  int     `json:"weight"`
	Share   float64 `json:"share"` // of new connections, 0-1
	RxBps   float64 `json:"rxBps"`
	TxBps   float64 `json:"txBps"`
	Checked int64   `json:"checked"`

	fails, oks   int
	rxPrev, txPr uint64
	ratesAt      time.Time
}

type Status struct {
	Enabled bool    `json:"enabled"`
	Active  int     `json:"active"` // healthy links in use
	Links   []Link  `json:"links"`
	RxBps   float64 `json:"rxBps"`
	TxBps   float64 `json:"txBps"`
}

type Manager struct {
	mu      sync.Mutex
	links   map[string]*Link
	enabled bool
	weights map[string]int
	routed  map[int]string // slot -> "dev gw" currently in its table
	planKey string
	// OnChange asks for the firewall to be rendered again.
	OnChange func()
	kick     chan struct{}
}

func New() *Manager {
	m := &Manager{links: map[string]*Link{}, routed: map[int]string{}, kick: make(chan struct{}, 1)}
	for _, c := range Candidates {
		m.links[c.Name] = &Link{Name: c.Name, Kind: c.Kind, Slot: c.Slot, Weight: 1}
	}
	return m
}

// Configure sets the user's choice; it takes effect at once.
func (m *Manager) Configure(enabled bool, weights map[string]int) {
	m.mu.Lock()
	m.enabled = enabled
	m.weights = weights
	for _, l := range m.links {
		l.Weight = clampWeight(weights[l.Name])
	}
	m.mu.Unlock()
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

func clampWeight(w int) int {
	if w < 1 {
		return 1
	}
	if w > 10 {
		return 10
	}
	return w
}

// Plan is what the firewall renders: nil when aggregation is off or fewer
// than two links exist (then the main routing table decides, as before).
func (m *Manager) Plan() []fw.BalanceLink {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.planLocked()
}

func (m *Manager) planLocked() []fw.BalanceLink {
	if !m.enabled {
		return nil
	}
	var out []fw.BalanceLink
	up := 0
	for _, c := range Candidates {
		l := m.links[c.Name]
		if l.Device == "" || !l.Up {
			continue
		}
		up++
		out = append(out, fw.BalanceLink{Dev: l.Device, Mark: markOf(l.Slot), Weight: l.Weight, Healthy: l.Healthy})
	}
	if up < 2 {
		return nil
	}
	return out
}

func planKey(p []fw.BalanceLink) string {
	var parts []string
	for _, l := range p {
		parts = append(parts, fmt.Sprintf("%s/%x/%d/%v", l.Dev, l.Mark, l.Weight, l.Healthy))
	}
	return strings.Join(parts, ",")
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{Enabled: m.enabled}
	plan := m.planLocked()
	total := 0
	for _, p := range plan {
		if p.Healthy {
			total += clampWeight(p.Weight)
			st.Active++
		}
	}
	for _, c := range Candidates {
		l := *m.links[c.Name]
		if total > 0 && l.Healthy && l.Up && plan != nil {
			l.Share = float64(l.Weight) / float64(total)
		}
		st.RxBps += l.RxBps
		st.TxBps += l.TxBps
		st.Links = append(st.Links, l)
	}
	return st
}

// Run refreshes the links every 10 s (and at once after Configure).
func (m *Manager) Run() {
	if env.Mock {
		m.mockRun()
		return
	}
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		m.tick()
		select {
		case <-t.C:
		case <-m.kick:
		}
	}
}

type ifStatus struct {
	Up       bool   `json:"up"`
	L3Device string `json:"l3_device"`
	IPv4     []struct {
		Address string `json:"address"`
	} `json:"ipv4-address"`
	Route []struct {
		Target  string `json:"target"`
		Nexthop string `json:"nexthop"`
	} `json:"route"`
}

var readIface = func(name string) (ifStatus, bool) {
	var st ifStatus
	out, err := env.Ubus("network.interface."+name, "status", "")
	if err != nil || json.Unmarshal([]byte(out), &st) != nil {
		return st, false
	}
	return st, true
}

func (m *Manager) tick() {
	type obs struct {
		up      bool
		dev, ip string
		gw      string
		rtt     int
		ok      bool
	}
	res := map[string]obs{}
	for _, c := range Candidates {
		st, ok := readIface(c.Name)
		o := obs{}
		if ok && st.Up && st.L3Device != "" && len(st.IPv4) > 0 {
			o.up, o.dev, o.ip = true, st.L3Device, st.IPv4[0].Address
			for _, r := range st.Route {
				if r.Target == "0.0.0.0" {
					o.gw = r.Nexthop
				}
			}
			o.rtt, o.ok = probe(o.dev)
		}
		res[c.Name] = o
	}

	m.mu.Lock()
	now := time.Now()
	for name, o := range res {
		l := m.links[name]
		if l.Device != o.dev {
			l.rxPrev, l.txPr, l.ratesAt = 0, 0, time.Time{}
		}
		fresh := o.up && (!l.Up || l.Device != o.dev) // link just appeared
		l.Up, l.Device, l.IP, l.Gateway, l.Checked = o.up, o.dev, o.ip, o.gw, now.Unix()
		l.Healthy, l.fails, l.oks = nextHealth(l.Healthy, l.fails, l.oks, o.up, o.ok, fresh)
		if o.ok {
			l.RTT = o.rtt
		} else if !o.up {
			l.RTT = 0
		}
		if o.dev != "" {
			rx := readUint("/sys/class/net/" + o.dev + "/statistics/rx_bytes")
			tx := readUint("/sys/class/net/" + o.dev + "/statistics/tx_bytes")
			if !l.ratesAt.IsZero() && rx >= l.rxPrev && tx >= l.txPr {
				sec := now.Sub(l.ratesAt).Seconds()
				if sec > 0 {
					l.RxBps, l.TxBps = float64(rx-l.rxPrev)/sec, float64(tx-l.txPr)/sec
				}
			}
			l.rxPrev, l.txPr, l.ratesAt = rx, tx, now
		} else {
			l.RxBps, l.TxBps = 0, 0
		}
	}
	enabled := m.enabled
	plan := m.planLocked()
	key := planKey(plan)
	changed := key != m.planKey
	m.planKey = key
	links := map[string]Link{}
	for k, v := range m.links {
		links[k] = *v
	}
	m.mu.Unlock()

	m.syncRoutes(enabled && plan != nil, links)
	if changed && m.OnChange != nil {
		env.Logf("multiwan: %s", describe(plan))
		m.OnChange()
	}
}

// nextHealth applies the hysteresis: a link that just appeared counts on
// its first good probe; one that failed needs two good probes in a row to
// come back; three failed probes (30 s) take it out, so a single lost ping
// doesn't reshuffle every connection.
func nextHealth(healthy bool, fails, oks int, up, ok, fresh bool) (bool, int, int) {
	switch {
	case !up:
		return false, 0, 0
	case ok:
		oks++
		if fresh || oks >= 2 {
			healthy = true
		}
		return healthy, 0, oks
	default:
		fails++
		if fails >= 3 || fresh {
			healthy = false
		}
		return healthy, fails, 0
	}
}

func describe(p []fw.BalanceLink) string {
	if p == nil {
		return "aggregation inactive"
	}
	var parts []string
	for _, l := range p {
		state := "down"
		if l.Healthy {
			state = "w" + strconv.Itoa(l.Weight)
		}
		parts = append(parts, l.Dev+":"+state)
	}
	return "links " + strings.Join(parts, " ")
}

var rttRe = regexp.MustCompile(`time=([0-9.]+) ?ms`)

// probe pings the targets out of one interface (SO_BINDTODEVICE).
var probe = func(dev string) (int, bool) {
	for _, t := range Targets {
		out, err := env.Run(4*time.Second, "ping", "-I", dev, "-c", "1", "-W", "2", t)
		if err == nil {
			if mm := rttRe.FindStringSubmatch(out); mm != nil {
				f, _ := strconv.ParseFloat(mm[1], 64)
				return int(f + 0.5), true
			}
			return 0, true
		}
	}
	return 0, false
}

// syncRoutes keeps one routing table and one ip rule per link while
// aggregation is active, and removes them otherwise.
func (m *Manager) syncRoutes(active bool, links map[string]Link) {
	for _, c := range Candidates {
		l := links[c.Name]
		table, pref := tableOf(c.Slot), prefOf(c.Slot)
		want := ""
		if active && l.Up && l.Device != "" {
			want = l.Device + " " + l.Gateway
		}
		if want == m.routed[c.Slot] {
			continue
		}
		run("ip", "-4", "rule", "del", "pref", strconv.Itoa(pref))
		run("ip", "-4", "route", "flush", "table", strconv.Itoa(table))
		if want == "" {
			delete(m.routed, c.Slot)
			continue
		}
		var err error
		if l.Gateway != "" {
			err = run("ip", "-4", "route", "replace", "default", "via", l.Gateway, "dev", l.Device, "table", strconv.Itoa(table))
		} else { // point-to-point (PPPoE)
			err = run("ip", "-4", "route", "replace", "default", "dev", l.Device, "table", strconv.Itoa(table))
		}
		if err == nil {
			err = run("ip", "-4", "rule", "add", "pref", strconv.Itoa(pref), "fwmark",
				fmt.Sprintf("0x%x/0x%x", markOf(c.Slot), fw.BalanceMask), "lookup", strconv.Itoa(table))
		}
		if err != nil {
			env.Logf("multiwan: %s: %v", l.Device, err)
			continue
		}
		// Replies of a connection on this link arrive here while the main
		// table points elsewhere: loose reverse-path filtering.
		os.WriteFile("/proc/sys/net/ipv4/conf/"+l.Device+"/rp_filter", []byte("2"), 0o644)
		m.routed[c.Slot] = want
	}
}

// Stop removes the tables and rules (aggregation off, shutdown).
func (m *Manager) Stop() {
	if env.Mock {
		return
	}
	m.syncRoutes(false, map[string]Link{})
}

func run(name string, args ...string) error {
	_, err := env.Run(5*time.Second, name, args...)
	return err
}

func readUint(path string) uint64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return n
}

// ---- mock ----

func (m *Manager) mockRun() {
	for {
		m.mu.Lock()
		devs := map[string]string{"wan": "eth1", "wwan": "wl-uplink", "wwan2": "wl-uplink2"}
		ips := map[string]string{"wan": "192.168.1.104", "wwan": "192.168.70.150", "wwan2": "192.168.71.150"}
		names := make([]string, 0, len(m.links))
		for n := range m.links {
			names = append(names, n)
		}
		sort.Strings(names)
		for i, n := range names {
			l := m.links[n]
			l.Up, l.Healthy, l.Device, l.IP = true, true, devs[n], ips[n]
			l.RTT = 30 + 15*i
			l.RxBps = float64(900_000 + 400_000*i)
			l.TxBps = float64(120_000 + 50_000*i)
			if !m.enabled && n != "wan" {
				l.RxBps, l.TxBps = 0, 0
			}
			l.Checked = time.Now().Unix()
		}
		m.mu.Unlock()
		select {
		case <-time.After(5 * time.Second):
		case <-m.kick:
		}
	}
}
