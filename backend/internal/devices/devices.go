// Package devices discovers LAN clients and tracks their live throughput.
package devices

import (
	"bufio"
	"encoding/json"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"toofanwrt/internal/env"
	"toofanwrt/internal/fw"
)

type Device struct {
	MAC       string  `json:"mac"`
	IP        string  `json:"ip"`
	Hostname  string  `json:"hostname"`
	Online    bool    `json:"online"`
	Link      string  `json:"link"` // wifi5 | wifi24 | lan | guest
	Signal    int     `json:"signal,omitempty"`
	RandomMAC bool    `json:"randomMac"`
	DownBps   float64 `json:"downBps"`
	UpBps     float64 `json:"upBps"`
	DownTotal uint64  `json:"downTotal"`
	UpTotal   uint64  `json:"upTotal"`
	LastSeen  int64   `json:"lastSeen"`
	GuessType string  `json:"guessType"`
	Vendor    string  `json:"vendor,omitempty"`
	// Wi-Fi association details (empty on cable).
	SSID         string  `json:"ssid,omitempty"`
	WifiGen      int     `json:"wifiGen,omitempty"` // 4, 5, 6 (802.11n/ac/ax)
	RxMbps       float64 `json:"rxMbps,omitempty"`  // link rate device -> router
	TxMbps       float64 `json:"txMbps,omitempty"`  // link rate router -> device
	Noise        int     `json:"noise,omitempty"`
	ConnectedSec int64   `json:"connectedSec,omitempty"`
	OnlineSince  int64   `json:"onlineSince,omitempty"`
	LeaseExpires int64   `json:"leaseExpires,omitempty"`
}

type Tracker struct {
	mu           sync.RWMutex
	devs         map[string]*Device // by MAC
	prevUp       map[string]uint64  // by IP
	prevDn       map[string]uint64
	prevAt       time.Time
	ipSet        string
	usage        *usage
	OnIPsChanged func() // firewall must add counters for new IPs
}

func NewTracker() *Tracker {
	return &Tracker{devs: map[string]*Device{}, prevUp: map[string]uint64{}, prevDn: map[string]uint64{}, usage: newUsage()}
}

func (t *Tracker) Run() {
	tk := time.NewTicker(2 * time.Second)
	n := 0
	for {
		if n%5 == 0 || env.Mock {
			t.discover()
		}
		t.sampleTraffic()
		n++
		<-tk.C
	}
}

func (t *Tracker) List() []Device {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Device, 0, len(t.devs))
	for _, d := range t.devs {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return ipLess(out[i].IP, out[j].IP)
	})
	return out
}

// IPs returns MAC -> current IP for online devices.
func (t *Tracker) IPs() map[string]string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	m := map[string]string{}
	for mac, d := range t.devs {
		if d.IP != "" {
			m[mac] = d.IP
		}
	}
	return m
}

func ipLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	if len(pa) != 4 || len(pb) != 4 {
		return a < b
	}
	for i := 0; i < 4; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return false
}

func isRandomMAC(mac string) bool {
	if len(mac) < 2 {
		return false
	}
	b, err := strconv.ParseUint(mac[:2], 16, 8)
	return err == nil && b&0x02 != 0
}

func guessType(host string, link string) string {
	h := strings.ToLower(host)
	switch {
	case containsAny(h, "iphone", "android", "galaxy", "redmi", "xiaomi", "pixel", "huawei", "phone", "oneplus", "poco"):
		return "phone"
	case containsAny(h, "ipad", "tab"):
		return "tablet"
	case containsAny(h, "macbook", "laptop", "notebook", "thinkpad", "desktop", "pc", "win", "dell", "hp-", "lenovo", "asus"):
		return "laptop"
	case containsAny(h, "tv", "bravia", "webos", "tizen", "chromecast", "firetv", "roku"):
		return "tv"
	case containsAny(h, "ps4", "ps5", "playstation", "xbox", "nintendo", "switch"):
		return "console"
	case containsAny(h, "cam", "esp", "tuya", "smart", "plug", "bulb", "iot", "shelly"):
		return "iot"
	}
	if link == "lan" {
		return "computer"
	}
	return "unknown"
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

// discover merges DHCP leases, the neighbour table and Wi-Fi associations.
func (t *Tracker) discover() {
	if env.Mock {
		t.mockDiscover()
		return
	}
	leases := readLeases()
	arp := readARP()
	wifi := readWifiClients()
	now := time.Now().Unix()

	t.mu.Lock()
	seen := map[string]bool{}
	get := func(mac string) *Device {
		d := t.devs[mac]
		if d == nil {
			d = &Device{MAC: mac, RandomMAC: isRandomMAC(mac)}
			t.devs[mac] = d
		}
		seen[mac] = true
		return d
	}
	for mac, l := range leases {
		d := get(mac)
		d.IP = l.ip
		d.LeaseExpires = l.expires
		if l.host != "" && l.host != "*" {
			d.Hostname = l.host
		}
	}
	for mac, a := range arp {
		d := get(mac)
		if d.IP == "" || a.reachable {
			d.IP = a.ip
		}
		if a.reachable {
			d.Online = true
			d.LastSeen = now
		}
		if d.Link == "" {
			d.Link = a.link
		}
	}
	for mac, w := range wifi {
		d := get(mac)
		d.Online, d.LastSeen, d.Link, d.Signal = true, now, w.link, w.signal
		d.SSID, d.WifiGen, d.RxMbps, d.TxMbps, d.Noise, d.ConnectedSec = w.ssid, w.gen, w.rxMbps, w.txMbps, w.noise, w.connected
	}
	for mac, d := range t.devs {
		_, inWifi := wifi[mac]
		a, inArp := arp[mac]
		if !inWifi && (!inArp || !a.reachable) {
			if now-d.LastSeen > 120 {
				d.Online = false
			}
		}
		if !inWifi && inArp && a.reachable {
			d.Link = a.link // on the bridge but not associated: wired
		}
		if !inWifi {
			d.SSID, d.WifiGen, d.RxMbps, d.TxMbps, d.Noise, d.ConnectedSec = "", 0, 0, 0, 0, 0
		}
		if d.Online && d.OnlineSince == 0 {
			d.OnlineSince = now
		} else if !d.Online {
			d.OnlineSince = 0
		}
		if d.Vendor == "" {
			d.Vendor = Vendor(mac)
		}
		d.GuessType = guessType(d.Hostname, d.Link)
		if !seen[mac] && now-d.LastSeen > 7*86400 {
			delete(t.devs, mac)
		}
	}
	ips := make([]string, 0, len(t.devs))
	for _, d := range t.devs {
		if d.IP != "" {
			ips = append(ips, d.IP)
		}
	}
	sort.Strings(ips)
	key := strings.Join(ips, ",")
	changed := key != t.ipSet
	t.ipSet = key
	t.mu.Unlock()
	if changed && t.OnIPsChanged != nil {
		t.OnIPsChanged()
	}
}

func (t *Tracker) sampleTraffic() {
	if env.Mock {
		t.mu.Lock()
		for _, d := range t.devs {
			if d.Online {
				base := map[string]float64{"laptop": 400000, "tv": 900000, "phone": 30000, "iot": 40}[d.GuessType]
				d.DownBps = base * rand.Float64()
				d.UpBps = base / 6 * rand.Float64()
				d.DownTotal += uint64(d.DownBps * 2)
				d.UpTotal += uint64(d.UpBps * 2)
				t.usage.add(d.MAC, uint64(d.DownBps*2), uint64(d.UpBps*2), time.Now())
			}
		}
		t.mu.Unlock()
		return
	}
	up, dn, err := fw.Counters()
	if err != nil {
		return
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	sec := now.Sub(t.prevAt).Seconds()
	for _, d := range t.devs {
		if d.IP == "" {
			continue
		}
		u, dw := up[d.IP], dn[d.IP]
		pu, pd := t.prevUp[d.IP], t.prevDn[d.IP]
		if !t.prevAt.IsZero() && sec > 0 {
			// Counters reset whenever the table is reloaded.
			du, dd := u-pu, dw-pd
			if u < pu {
				du = u
			}
			if dw < pd {
				dd = dw
			}
			d.UpBps, d.DownBps = float64(du)/sec, float64(dd)/sec
			if u < pu {
				d.UpBps = 0
			}
			if dw < pd {
				d.DownBps = 0
			}
			d.UpTotal += du
			d.DownTotal += dd
			t.usage.add(d.MAC, dd, du, now)
		}
	}
	t.prevUp, t.prevDn, t.prevAt = up, dn, now
}

type lease struct {
	ip, host string
	expires  int64
}

func readLeases() map[string]lease {
	out := map[string]lease{}
	f, err := os.Open("/tmp/dhcp.leases")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) >= 4 && strings.Count(fs[2], ".") == 3 {
			exp, _ := strconv.ParseInt(fs[0], 10, 64) // 0 = static (infinite)
			out[strings.ToLower(fs[1])] = lease{ip: fs[2], host: fs[3], expires: exp}
		}
	}
	return out
}

type arpEntry struct {
	ip        string
	link      string
	reachable bool
}

// readARP uses "ip neigh" for reachability state (the /proc table lacks it).
func readARP() map[string]arpEntry {
	out := map[string]arpEntry{}
	txt, err := env.Run(5*time.Second, "ip", "-4", "neigh", "show")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(txt, "\n") {
		fs := strings.Fields(line)
		// 192.168.1.10 dev br-lan lladdr aa:bb:.. REACHABLE
		if len(fs) < 5 {
			continue
		}
		var dev, mac string
		for i := 0; i+1 < len(fs); i++ {
			switch fs[i] {
			case "dev":
				dev = fs[i+1]
			case "lladdr":
				mac = strings.ToLower(fs[i+1])
			}
		}
		if mac == "" || !strings.HasPrefix(dev, "br-") {
			continue
		}
		state := fs[len(fs)-1]
		link := "lan"
		if dev != "br-lan" {
			link = "guest"
		}
		out[mac] = arpEntry{ip: fs[0], link: link,
			reachable: state == "REACHABLE" || state == "DELAY" || state == "PROBE"}
	}
	return out
}

type wifiClient struct {
	link      string
	signal    int
	ssid      string
	gen       int
	rxMbps    float64
	txMbps    float64
	noise     int
	connected int64
}

type iwRate struct {
	Rate int  `json:"rate"` // kbit/s
	HT   bool `json:"ht"`
	VHT  bool `json:"vht"`
	HE   bool `json:"he"`
}

// readWifiClients asks rpcd's iwinfo module for every AP interface.
func readWifiClients() map[string]wifiClient {
	out := map[string]wifiClient{}
	devsJSON, err := env.Ubus("iwinfo", "devices", "")
	if err != nil {
		return out
	}
	var devs struct {
		Devices []string `json:"devices"`
	}
	json.Unmarshal([]byte(devsJSON), &devs)
	for _, dev := range devs.Devices {
		infoJSON, err := env.Ubus("iwinfo", "info", `{"device":"`+dev+`"}`)
		if err != nil {
			continue
		}
		var info struct {
			Mode      string `json:"mode"`
			Frequency int    `json:"frequency"`
			SSID      string `json:"ssid"`
		}
		json.Unmarshal([]byte(infoJSON), &info)
		if info.Mode != "Master" {
			continue
		}
		link := "wifi24"
		if info.Frequency > 4000 {
			link = "wifi5"
		}
		if isGuestIface(dev) {
			link = "guest"
		}
		assocJSON, err := env.Ubus("iwinfo", "assoclist", `{"device":"`+dev+`"}`)
		if err != nil {
			continue
		}
		var assoc struct {
			Results []struct {
				MAC       string `json:"mac"`
				Signal    int    `json:"signal"`
				Noise     int    `json:"noise"`
				Connected int64  `json:"connected_time"`
				Rx        iwRate `json:"rx"`
				Tx        iwRate `json:"tx"`
			} `json:"results"`
		}
		json.Unmarshal([]byte(assocJSON), &assoc)
		for _, r := range assoc.Results {
			gen := 0
			switch {
			case r.Rx.HE || r.Tx.HE:
				gen = 6
			case r.Rx.VHT || r.Tx.VHT:
				gen = 5
			case r.Rx.HT || r.Tx.HT:
				gen = 4
			}
			out[strings.ToLower(r.MAC)] = wifiClient{link: link, signal: r.Signal, ssid: info.SSID, gen: gen,
				rxMbps: float64(r.Rx.Rate) / 1000, txMbps: float64(r.Tx.Rate) / 1000, noise: r.Noise, connected: r.Connected}
		}
	}
	return out
}

var guestIfaces = map[string]bool{}
var guestMu sync.Mutex

// SetGuestIfaces records which wireless netdevs belong to the guest network.
func SetGuestIfaces(names []string) {
	guestMu.Lock()
	defer guestMu.Unlock()
	guestIfaces = map[string]bool{}
	for _, n := range names {
		guestIfaces[n] = true
	}
}

func isGuestIface(n string) bool {
	guestMu.Lock()
	defer guestMu.Unlock()
	return guestIfaces[n]
}

func (t *Tracker) mockDiscover() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.devs) > 0 {
		return
	}
	now := time.Now().Unix()
	add := func(mac, ip, host, link string, online bool) {
		d := &Device{MAC: mac, IP: ip, Hostname: host, Link: link, Online: online, Vendor: Vendor(mac),
			RandomMAC: isRandomMAC(mac), LastSeen: now, GuessType: guessType(host, link), LeaseExpires: now + 30000}
		if strings.HasPrefix(link, "wifi") {
			d.Signal, d.Noise, d.SSID, d.WifiGen = -50-rand.Intn(25), -95, "ToofanWrt", 5
			d.RxMbps, d.TxMbps, d.ConnectedSec = 390, 433.3, 5400
			if link == "wifi24" {
				d.SSID, d.WifiGen, d.RxMbps, d.TxMbps = "ToofanWrt", 4, 65, 72.2
			}
		}
		if online {
			d.OnlineSince = now - 5400
		}
		t.devs[mac] = d
	}
	add("f0:d1:a9:42:9c:01", "192.168.1.238", "Galaxy-S23", "wifi5", true)
	add("de:41:7a:0b:33:12", "192.168.1.123", "MacBook-Air", "wifi5", true)
	add("62:b0:4c:91:aa:5e", "192.168.1.195", "iPhone", "wifi5", true)
	add("00:1e:c2:7d:44:90", "192.168.1.202", "LG-webOS-TV", "lan", true)
	add("c8:2b:96:10:7e:3a", "192.168.1.215", "ESP-Camera", "wifi24", true)
	add("f4:5c:89:aa:01:77", "192.168.1.140", "PS5", "lan", false)
}
