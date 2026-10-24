package api

import (
	"net/http"
	"regexp"
	"sync"

	"toofanwrt/internal/config"
	"toofanwrt/internal/gaming"
	"toofanwrt/internal/proxy"
	"toofanwrt/internal/wifi"
)

var (
	natMu   sync.Mutex
	lastNAT *gaming.NATResult
)

func (s *Server) phase2Routes(api *http.ServeMux) {
	api.HandleFunc("GET /api/gaming", s.gamingGet)
	api.HandleFunc("POST /api/gaming/nat", s.gamingNAT)
	api.HandleFunc("POST /api/gaming/upnp", s.gamingUPnP)
	api.HandleFunc("POST /api/gaming/sqm", s.gamingSQM)
	api.HandleFunc("GET /api/gaming/bloat", s.bloatGet)
	api.HandleFunc("POST /api/gaming/bloat", s.bloatStart)

	api.HandleFunc("GET /api/led", s.ledGet)
	api.HandleFunc("POST /api/led", s.ledSet)

	api.HandleFunc("POST /api/wifi/scan", s.wifiScan)
	api.HandleFunc("POST /api/wifi/smart", s.wifiSmart)

	api.HandleFunc("GET /api/diag", s.diag)
	api.HandleFunc("GET /api/incidents", s.incidents)
	api.HandleFunc("POST /api/security", s.security)
}

// ---- gaming ----

func (s *Server) gamingGet(w http.ResponseWriter, r *http.Request) {
	cfg := s.core.Store.Get()
	natMu.Lock()
	nat := lastNAT
	natMu.Unlock()
	var prio []string
	for mac, d := range cfg.Devices {
		if d.Priority {
			prio = append(prio, mac)
		}
	}
	writeJSON(w, map[string]any{
		"nat":          nat,
		"upnp":         gaming.UPnP(),
		"sqm":          cfg.SQM,
		"sqmAvailable": gaming.SQMAvailable(),
		"bloat":        s.core.Bloat.Get(),
		"priority":     prio,
		"latency":      s.core.Quality.Last(),
	})
}

func (s *Server) gamingNAT(w http.ResponseWriter, r *http.Request) {
	res := gaming.DetectNAT(s.core.Sampler.WAN().IP)
	natMu.Lock()
	lastNAT = &res
	natMu.Unlock()
	writeJSON(w, res)
}

func (s *Server) gamingUPnP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !gaming.UPnP().Installed {
		writeErr(w, http.StatusBadRequest, "miniupnpd-nftables is not installed")
		return
	}
	respond(w, gaming.SetUPnP(in.Enabled))
}

func (s *Server) gamingSQM(w http.ResponseWriter, r *http.Request) {
	var in config.SQM
	if !decode(w, r, &in) {
		return
	}
	if in.Enabled && (in.DownMbps <= 0 || in.UpMbps <= 0 || in.DownMbps > 2000 || in.UpMbps > 2000) {
		writeErr(w, http.StatusBadRequest, "enter download and upload speeds in Mbps")
		return
	}
	if in.Enabled && !gaming.SQMAvailable() {
		writeErr(w, http.StatusBadRequest, "SQM needs tc-tiny, kmod-sched-cake and kmod-ifb")
		return
	}
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		c.SQM = in
		return nil
	}))
}

func (s *Server) bloatGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.core.Bloat.Get())
}

func (s *Server) bloatStart(w http.ResponseWriter, r *http.Request) {
	port := 0
	if s.core.Proxy.Mode() == proxy.ModeConnected {
		port = proxy.SocksPort
	}
	if !s.core.Bloat.Start(port) {
		writeErr(w, http.StatusConflict, "a test is already running")
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- LED ----

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (s *Server) ledGet(w http.ResponseWriter, r *http.Request) {
	mbps, src := s.core.SpeedEstimate()
	writeJSON(w, map[string]any{
		"config":    s.core.Store.Get().LED,
		"supported": s.core.LED.Supported(),
		"current":   s.core.LED.Current(),
		"speedMbps": mbps, "speedFrom": src,
	})
}

func (s *Server) ledSet(w http.ResponseWriter, r *http.Request) {
	var in config.LED
	if !decode(w, r, &in) {
		return
	}
	switch in.Mode {
	case "auto", "color", "ping", "speed", "off", "default":
	default:
		writeErr(w, http.StatusBadRequest, "invalid mode")
		return
	}
	if !hexColor.MatchString(in.Color) || in.Brightness < 5 || in.Brightness > 100 ||
		!hhmm.MatchString(in.NightFrom) || !hhmm.MatchString(in.NightTo) ||
		in.SpeedGood < 0 || in.SpeedMid < 0 || in.SpeedGood > 10000 || (in.SpeedGood > 0 && in.SpeedMid >= in.SpeedGood) {
		writeErr(w, http.StatusBadRequest, "invalid values")
		return
	}
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		c.LED = in
		return nil
	}))
}

// ---- wifi smart ----

func (s *Server) wifiScan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Band  string `json:"band"`
		Apply bool   `json:"apply"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Band != "2g" && in.Band != "5g" {
		writeErr(w, http.StatusBadRequest, "band must be 2g or 5g")
		return
	}
	res, err := wifi.Optimize(in.Band, in.Apply)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, res)
}

func (s *Server) wifiSmart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		c.SmartWiFi = in.Enabled
		return nil
	}))
}

// ---- diagnostics / security ----

func (s *Server) diag(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.core.Diagnose())
}

func (s *Server) incidents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.core.Incidents.List())
}

func (s *Server) security(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LockWAN bool `json:"lockWan"`
	}
	if !decode(w, r, &in) {
		return
	}
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		c.LockWAN = in.LockWAN
		return nil
	}))
}
