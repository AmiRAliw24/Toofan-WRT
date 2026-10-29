// Package config holds the persistent panel settings, stored as one JSON file.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Server struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Link     string `json:"link"`
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Network  string `json:"network"`
	Security string `json:"security"`
	SubID    string `json:"subId,omitempty"`
	// Last test result; LatencyMs < 0 means the test failed.
	LatencyMs int    `json:"latencyMs"`
	TestedAt  int64  `json:"testedAt,omitempty"`
	TestError string `json:"testError,omitempty"`
}

type Subscription struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
	Error     string `json:"error,omitempty"`
	Count     int    `json:"count"`
}

// OpenVPNProfile is an imported .ovpn file; its server entry links to it
// as openvpn://<id>.
type OpenVPNProfile struct {
	ID       string `json:"id"`
	Config   string `json:"config"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// OpenVPNProfiles accepts the old single-object form (version 1) as empty.
type OpenVPNProfiles []OpenVPNProfile

func (p *OpenVPNProfiles) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || b[0] != '[' {
		*p = OpenVPNProfiles{}
		return nil
	}
	var list []OpenVPNProfile
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}
	*p = list
	return nil
}

type Proxy struct {
	Enabled       bool           `json:"enabled"`
	Engine        string         `json:"engine"` // xray | wireguard | openvpn
	Servers       []Server       `json:"servers"`
	Subscriptions []Subscription `json:"subscriptions"`
	ActiveID      string         `json:"activeId"`
	PreferredID   string         `json:"preferredId"`
	SplitIran     bool           `json:"splitIran"` // kept in sync with IranMode for older readers
	IranMode      string         `json:"iranMode"`  // direct | tunnel | block
	KillSwitch    bool           `json:"killSwitch"`
	BlockIPv6     bool           `json:"blockIPv6"`
	GuestProxy    bool           `json:"guestProxy"`
	AutoBest      bool           `json:"autoBest"`
	SubAutoUpdate bool           `json:"subAutoUpdate"`
	// Periodic tunnel health check (watchdog). Off = no periodic probes, so
	// no automatic failover; a crashed engine is still restarted.
	HealthPaused   bool            `json:"healthPaused"`
	HealthInterval int             `json:"healthInterval"` // seconds
	OpenVPN        OpenVPNProfiles `json:"openvpn"`
}

type Schedule struct {
	ID      string   `json:"id"`
	MACs    []string `json:"macs"`
	Days    []int    `json:"days"` // 0=Sunday .. 6=Saturday
	From    string   `json:"from"` // HH:MM, internet blocked from..to
	To      string   `json:"to"`
	Enabled bool     `json:"enabled"`
}

type Device struct {
	Name      string `json:"name,omitempty"`
	Type      string `json:"type,omitempty"`
	Blocked   bool   `json:"blocked,omitempty"`
	Direct    bool   `json:"direct,omitempty"` // bypass the proxy
	Priority  bool   `json:"priority,omitempty"`
	DownKbps  int    `json:"downKbps,omitempty"` // 0 = unlimited
	UpKbps    int    `json:"upKbps,omitempty"`
	FirstSeen int64  `json:"firstSeen,omitempty"`
}

type DNS struct {
	Provider string `json:"provider"` // id from the provider list, "isp" = modem/ISP DNS
	Custom   string `json:"custom"`
	Mode     string `json:"mode"` // udp | doh
	BlockAds bool   `json:"blockAds"`
	LogSites bool   `json:"logSites"`
}

type LED struct {
	Mode       string `json:"mode"` // auto | color | ping | speed | off | default
	Color      string `json:"color"`
	Brightness int    `json:"brightness"`
	NightOff   bool   `json:"nightOff"`
	NightFrom  string `json:"nightFrom"`
	NightTo    string `json:"nightTo"`
	SpeedGood  int    `json:"speedGood"` // Mbps: green from here (speed mode)
	SpeedMid   int    `json:"speedMid"`  // Mbps: yellow from here, red below
}

type SQM struct {
	Enabled  bool `json:"enabled"`
	DownMbps int  `json:"downMbps"`
	UpMbps   int  `json:"upMbps"`
}

type Config struct {
	Lang      string            `json:"lang"`
	Proxy     Proxy             `json:"proxy"`
	Devices   map[string]Device `json:"devices"` // keyed by lower-case MAC
	Schedules []Schedule        `json:"schedules"`
	DNS       DNS               `json:"dns"`
	LED       LED               `json:"led"`
	LockWAN   bool              `json:"lockWan"` // panel/SSH unreachable from WAN
	SmartWiFi bool              `json:"smartWifi"`
	SQM       SQM               `json:"sqm"`
	UpdateURL string            `json:"updateUrl"` // release manifest (JSON)
	// WiFiRoutes: wifi-iface section -> "direct" for networks that bypass the tunnel.
	WiFiRoutes map[string]string `json:"wifiRoutes"`
}

func Default() *Config {
	return &Config{
		Lang: "fa",
		Proxy: Proxy{
			Engine:        "xray",
			SplitIran:     true,
			IranMode:      IranDirect,
			KillSwitch:    false,
			BlockIPv6:     true,
			AutoBest:      false,
			SubAutoUpdate: true,
		},
		Devices: map[string]Device{},
		DNS:     DNS{Provider: "isp", Mode: "udp"},
		LockWAN: true,
		LED:     LED{Mode: "auto", Color: "#00b894", Brightness: 60, NightFrom: "23:30", NightTo: "07:00", SpeedGood: 20, SpeedMid: 5},
	}
}

// Store guards the config and persists it on every change.
type Store struct {
	path string
	mu   sync.RWMutex
	cfg  *Config
	subs []func()
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, cfg: Default()}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, s.cfg); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	normalize(s.cfg)
	return s, nil
}

// Get returns a deep copy, safe to read without holding the lock.
func (s *Store) Get() *Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.cfg)
}

// Update applies fn to the config, saves it, and notifies listeners.
func (s *Store) Update(fn func(c *Config) error) error {
	s.mu.Lock()
	next := clone(s.cfg)
	if err := fn(next); err != nil {
		s.mu.Unlock()
		return err
	}
	if err := s.save(next); err != nil {
		s.mu.Unlock()
		return err
	}
	s.cfg = next
	subs := append([]func(){}, s.subs...)
	s.mu.Unlock()
	for _, f := range subs {
		f()
	}
	return nil
}

// UpdateQuiet saves without notifying listeners (for bookkeeping like test results).
func (s *Store) UpdateQuiet(fn func(c *Config)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.cfg)
	fn(next)
	if s.save(next) == nil {
		s.cfg = next
	}
}

func (s *Store) OnChange(f func()) {
	s.mu.Lock()
	s.subs = append(s.subs, f)
	s.mu.Unlock()
}

func (s *Store) save(c *Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func clone(c *Config) *Config {
	b, _ := json.Marshal(c)
	out := &Config{}
	_ = json.Unmarshal(b, out)
	normalize(out)
	return out
}

// normalize replaces nil lists with empty ones so the API never sends null
// where the UI expects an array.
func normalize(c *Config) {
	if c.Devices == nil {
		c.Devices = map[string]Device{}
	}
	if c.Proxy.Servers == nil {
		c.Proxy.Servers = []Server{}
	}
	if c.Proxy.Subscriptions == nil {
		c.Proxy.Subscriptions = []Subscription{}
	}
	if c.Proxy.OpenVPN == nil {
		c.Proxy.OpenVPN = OpenVPNProfiles{}
	}
	if c.Schedules == nil {
		c.Schedules = []Schedule{}
	}
	if c.WiFiRoutes == nil {
		c.WiFiRoutes = map[string]string{}
	}
	switch c.Proxy.IranMode {
	case IranDirect, IranTunnel, IranBlock:
	default: // configs from before the mode existed
		c.Proxy.IranMode = IranTunnel
		if c.Proxy.SplitIran {
			c.Proxy.IranMode = IranDirect
		}
	}
	c.Proxy.SplitIran = c.Proxy.IranMode == IranDirect
	if c.Proxy.HealthInterval < MinHealthInterval || c.Proxy.HealthInterval > MaxHealthInterval {
		c.Proxy.HealthInterval = DefaultHealthInterval
	}
}

// Health check interval bounds, in seconds.
const (
	DefaultHealthInterval = 15
	MinHealthInterval     = 10
	MaxHealthInterval     = 600
)

// What happens to traffic for Iranian sites while the tunnel is on.
const (
	IranDirect = "direct" // straight out on the local line
	IranTunnel = "tunnel" // through the tunnel like everything else
	IranBlock  = "block"  // not reachable at all
)
