// Package system samples CPU, memory, uptime and WAN throughput.
package system

import (
	"bufio"
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"toofanwrt/internal/env"
)

type Stats struct {
	Model       string     `json:"model"`
	Hostname    string     `json:"hostname"`
	Firmware    string     `json:"firmware"`
	Kernel      string     `json:"kernel"`
	UptimeSec   int64      `json:"uptimeSec"`
	CPUPercent  float64    `json:"cpuPercent"`
	Cores       int        `json:"cores"`
	Load        [3]float64 `json:"load"`
	MemTotalKB  int64      `json:"memTotalKb"`
	MemAvailKB  int64      `json:"memAvailKb"`
	DiskTotalKB int64      `json:"diskTotalKb"`
	DiskFreeKB  int64      `json:"diskFreeKb"`
	TempC       float64    `json:"tempC,omitempty"`
	TempSensor  string     `json:"tempSensor,omitempty"` // which sensor TempC came from
	Temps       []Temp     `json:"temps,omitempty"`      // every sensor found
}

type WAN struct {
	Up        bool     `json:"up"`
	Proto     string   `json:"proto"`
	IP        string   `json:"ip"`
	Gateway   string   `json:"gateway"`
	DNS       []string `json:"dns"`
	Device    string   `json:"device"`
	RxBps     float64  `json:"rxBps"` // bytes per second
	TxBps     float64  `json:"txBps"`
	RxTotal   uint64   `json:"rxTotal"`
	TxTotal   uint64   `json:"txTotal"`
	UptimeSec int64    `json:"uptimeSec"`
}

type Sampler struct {
	mu   sync.RWMutex
	cpu  float64
	wan  WAN
	info Stats

	prevIdle, prevTotal uint64
	prevRx, prevTx      uint64
	prevAt              time.Time
}

func NewSampler() *Sampler {
	s := &Sampler{}
	s.info = staticInfo()
	return s
}

func (s *Sampler) Run() {
	t := time.NewTicker(2 * time.Second)
	for {
		s.tick()
		<-t.C
	}
}

func (s *Sampler) Stats() Stats {
	s.mu.RLock()
	st := s.info
	st.CPUPercent = s.cpu
	s.mu.RUnlock()
	fillDynamic(&st)
	return st
}

func (s *Sampler) WAN() WAN {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.wan
}

func (s *Sampler) tick() {
	if env.Mock {
		s.mockTick()
		return
	}
	idle, total := readCPU()
	w := readWAN()
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prevTotal > 0 && total > s.prevTotal {
		dt := float64(total - s.prevTotal)
		di := float64(idle - s.prevIdle)
		s.cpu = math.Round((1-di/dt)*1000) / 10
	}
	s.prevIdle, s.prevTotal = idle, total
	if !s.prevAt.IsZero() && w.Device == s.wan.Device {
		sec := now.Sub(s.prevAt).Seconds()
		if w.RxTotal >= s.prevRx && w.TxTotal >= s.prevTx && sec > 0 {
			w.RxBps = float64(w.RxTotal-s.prevRx) / sec
			w.TxBps = float64(w.TxTotal-s.prevTx) / sec
		}
	}
	s.prevRx, s.prevTx, s.prevAt = w.RxTotal, w.TxTotal, now
	s.wan = w
}

func (s *Sampler) mockTick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cpu = math.Round((8+rand.Float64()*14)*10) / 10
	s.wan = WAN{
		Up: true, Proto: "dhcp", IP: "192.168.1.104", Gateway: "192.168.1.1",
		DNS: []string{"192.168.1.1"}, Device: "wan",
		RxBps: 40000 + rand.Float64()*900000, TxBps: 8000 + rand.Float64()*90000,
		UptimeSec: 3*86400 + 10*3600,
	}
}

func readCPU() (idle, total uint64) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return
	}
	fields := strings.Fields(sc.Text())
	for i, v := range fields[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
		if i == 3 || i == 4 { // idle + iowait
			idle += n
		}
	}
	return
}

type ifaceStatus struct {
	Up       bool   `json:"up"`
	Proto    string `json:"proto"`
	L3Device string `json:"l3_device"`
	Uptime   int64  `json:"uptime"`
	IPv4     []struct {
		Address string `json:"address"`
	} `json:"ipv4-address"`
	Route []struct {
		Target  string `json:"target"`
		Nexthop string `json:"nexthop"`
	} `json:"route"`
	DNS []string `json:"dns-server"`
}

// WANStatus reads the active uplink from netifd: the WAN cable, or the
// Wi-Fi uplink ("wwan") when the cable isn't connected.
func WANStatus() (ifaceStatus, error) {
	var first ifaceStatus
	var firstErr error
	for i, name := range []string{"wan", "wwan"} {
		var st ifaceStatus
		out, err := env.Ubus("network.interface."+name, "status", "")
		if err == nil {
			err = json.Unmarshal([]byte(out), &st)
		}
		if i == 0 {
			first, firstErr = st, err
		}
		if err == nil && st.Up {
			return st, nil
		}
	}
	return first, firstErr
}

func readWAN() WAN {
	st, err := WANStatus()
	if err != nil {
		return WAN{}
	}
	w := WAN{Up: st.Up, Proto: st.Proto, Device: st.L3Device, UptimeSec: st.Uptime, DNS: st.DNS}
	if len(st.IPv4) > 0 {
		w.IP = st.IPv4[0].Address
	}
	for _, r := range st.Route {
		if r.Target == "0.0.0.0" {
			w.Gateway = r.Nexthop
		}
	}
	if w.Device != "" {
		w.RxTotal = readUint("/sys/class/net/" + w.Device + "/statistics/rx_bytes")
		w.TxTotal = readUint("/sys/class/net/" + w.Device + "/statistics/tx_bytes")
	}
	return w
}

func readUint(path string) uint64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return n
}

func staticInfo() Stats {
	st := Stats{Cores: runtime.NumCPU()}
	if env.Mock {
		st.Model = "Google WiFi (Gale)"
		st.Hostname = "OpenWrt"
		st.Firmware = "OpenWrt 23.05.4 r24012-d8dd03c46f"
		st.Kernel = "5.15.162"
		st.Cores = 4
		return st
	}
	out, err := env.Ubus("system", "board", "")
	if err == nil {
		var b struct {
			Model    string `json:"model"`
			Hostname string `json:"hostname"`
			Kernel   string `json:"kernel"`
			Release  struct {
				Description string `json:"description"`
			} `json:"release"`
		}
		if json.Unmarshal([]byte(out), &b) == nil {
			st.Model, st.Hostname, st.Kernel, st.Firmware = b.Model, b.Hostname, b.Kernel, b.Release.Description
		}
	}
	return st
}

func fillDynamic(st *Stats) {
	if env.Mock {
		st.UptimeSec = 3*86400 + 10*3600 + int64(time.Now().Unix()%3600)
		st.Load = [3]float64{0.42, 0.37, 0.31}
		st.MemTotalKB, st.MemAvailKB = 506000, 341000
		st.DiskTotalKB, st.DiskFreeKB = 3800000, 3550000
		st.TempC, st.TempSensor = 52, "cpu"
		st.Temps = []Temp{{Name: "cpu", C: 52}, {Name: "ath10k_hwmon (phy0)", C: 47}, {Name: "ath10k_hwmon (phy1)", C: 49}}
		return
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		f, _ := strconv.ParseFloat(strings.Fields(string(b))[0], 64)
		st.UptimeSec = int64(f)
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		fs := strings.Fields(string(b))
		for i := 0; i < 3 && i < len(fs); i++ {
			st.Load[i], _ = strconv.ParseFloat(fs[i], 64)
		}
	}
	if f, err := os.Open("/proc/meminfo"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fs := strings.Fields(sc.Text())
			if len(fs) < 2 {
				continue
			}
			n, _ := strconv.ParseInt(fs[1], 10, 64)
			switch fs[0] {
			case "MemTotal:":
				st.MemTotalKB = n
			case "MemAvailable:":
				st.MemAvailKB = n
			}
		}
		f.Close()
	}
	st.DiskTotalKB, st.DiskFreeKB = diskUsage("/overlay")
	if st.DiskTotalKB == 0 {
		st.DiskTotalKB, st.DiskFreeKB = diskUsage("/")
	}
	st.Temps = readTemps()
	st.TempC, st.TempSensor = pickTemp(st.Temps)
}

type Temp struct {
	Name string  `json:"name"`
	C    float64 `json:"c"`
}

// readTemps collects every temperature sensor: thermal zones (SoC/CPU, when
// the device tree has them) and hwmon devices (on IPQ40xx routers such as
// Google WiFi usually only the ath10k radios report one).
func readTemps() []Temp {
	var out []Temp
	add := func(name, path string) {
		b, err := os.ReadFile(path)
		if err != nil {
			return
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if err != nil {
			return
		}
		if v > 1000 { // millidegrees
			v /= 1000
		}
		if v <= 0 || v >= 150 { // missing or bogus reading
			return
		}
		out = append(out, Temp{Name: name, C: math.Round(v*10) / 10})
	}
	zones, _ := filepath.Glob("/sys/class/thermal/thermal_zone*")
	for _, z := range zones {
		name := strings.TrimSpace(readStr(z + "/type"))
		if name == "" {
			name = filepath.Base(z)
		}
		add(name, z+"/temp")
	}
	mons, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, h := range mons {
		name := strings.TrimSpace(readStr(h + "/name"))
		if name == "" {
			name = filepath.Base(h)
		}
		// ath10k names every radio the same; tell them apart by phy.
		if phys, _ := filepath.Glob(h + "/device/ieee80211/phy*"); len(phys) == 1 {
			name += " (" + filepath.Base(phys[0]) + ")"
		}
		inputs, _ := filepath.Glob(h + "/temp*_input")
		for _, in := range inputs {
			add(name, in)
		}
	}
	return out
}

// pickTemp chooses the headline temperature: a CPU/SoC sensor when there
// is one, otherwise the hottest sensor.
func pickTemp(ts []Temp) (float64, string) {
	best := -1
	for i, t := range ts {
		n := strings.ToLower(t.Name)
		if strings.Contains(n, "cpu") || strings.Contains(n, "soc") || strings.Contains(n, "tsens") {
			return t.C, t.Name
		}
		if best < 0 || t.C > ts[best].C {
			best = i
		}
	}
	if best < 0 {
		return 0, ""
	}
	return ts[best].C, ts[best].Name
}

func readStr(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

var (
	tzMu      sync.Mutex
	tzOffset  int
	tzChecked time.Time
)

// TZOffsetMinutes is the router's offset from UTC as OpenWrt applies it
// (/etc/TZ, which Go's time.Local does not read), refreshed every minute.
func TZOffsetMinutes() int {
	if env.Mock {
		return 210 // Asia/Tehran
	}
	tzMu.Lock()
	defer tzMu.Unlock()
	if time.Since(tzChecked) < time.Minute {
		return tzOffset
	}
	tzChecked = time.Now()
	if out, err := env.Run(3*time.Second, "date", "+%z"); err == nil {
		tzOffset = parseTZ(strings.TrimSpace(out))
	}
	return tzOffset
}

// parseTZ turns "+0330" / "-0500" into minutes.
func parseTZ(z string) int {
	if len(z) != 5 || (z[0] != '+' && z[0] != '-') {
		return 0
	}
	h, err1 := strconv.Atoi(z[1:3])
	m, err2 := strconv.Atoi(z[3:5])
	if err1 != nil || err2 != nil {
		return 0
	}
	v := h*60 + m
	if z[0] == '-' {
		v = -v
	}
	return v
}
