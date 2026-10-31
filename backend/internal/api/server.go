// Package api exposes the panel's JSON API and serves the web UI.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"toofanwrt/internal/config"
	"toofanwrt/internal/core"
	"toofanwrt/internal/devices"
	"toofanwrt/internal/env"
	"toofanwrt/internal/proxy"
	"toofanwrt/internal/system"
	"toofanwrt/internal/update"
	"toofanwrt/internal/wifi"
)

type Server struct {
	core    *core.Core
	sess    *sessions
	web     fs.FS
	Version string

	updOnce sync.Once
	upd     *update.Updater
}

func New(c *core.Core, web fs.FS, version string) *Server {
	return &Server{core: c, sess: newSessions(), web: web, Version: version}
}

func (s *Server) Handler() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/overview", s.overview)
	api.HandleFunc("GET /api/system", s.system)
	api.HandleFunc("POST /api/system/reboot", s.reboot)
	api.HandleFunc("POST /api/system/password", s.setPassword)
	api.HandleFunc("GET /api/system/backup", s.backup)
	api.HandleFunc("GET /api/events", s.events)
	api.HandleFunc("POST /api/settings", s.settings)

	api.HandleFunc("GET /api/proxy", s.proxyGet)
	api.HandleFunc("POST /api/proxy/settings", s.proxySettings)
	api.HandleFunc("POST /api/proxy/servers", s.proxyAddServers)
	api.HandleFunc("DELETE /api/proxy/servers/{id}", s.proxyDeleteServer)
	api.HandleFunc("POST /api/proxy/servers/delete", s.proxyDeleteServers)
	api.HandleFunc("POST /api/proxy/select", s.proxySelect)
	api.HandleFunc("POST /api/proxy/openvpn", s.proxyAddOpenVPN)
	api.HandleFunc("POST /api/proxy/test", s.proxyTest)
	api.HandleFunc("POST /api/proxy/subs", s.proxyAddSub)
	api.HandleFunc("POST /api/proxy/subs/{id}/update", s.proxyUpdateSub)
	api.HandleFunc("DELETE /api/proxy/subs/{id}", s.proxyDeleteSub)
	api.HandleFunc("GET /api/proxy/logs", s.proxyLogs)

	api.HandleFunc("GET /api/devices", s.devicesGet)
	api.HandleFunc("POST /api/devices/{mac}", s.deviceSet)
	api.HandleFunc("GET /api/devices/{mac}/info", s.deviceInfo)
	api.HandleFunc("POST /api/devices/{mac}/wake", s.deviceWake)
	api.HandleFunc("GET /api/sites", s.sitesGet)
	api.HandleFunc("POST /api/sites", s.sitesSet)
	api.HandleFunc("DELETE /api/sites", s.sitesClear)
	api.HandleFunc("GET /api/schedules", s.schedulesGet)
	api.HandleFunc("POST /api/schedules", s.scheduleSave)
	api.HandleFunc("DELETE /api/schedules/{id}", s.scheduleDelete)

	api.HandleFunc("GET /api/wifi", s.wifiGet)
	api.HandleFunc("POST /api/wifi/band", s.wifiSetBand)
	api.HandleFunc("POST /api/wifi/guest", s.wifiSetGuest)

	api.HandleFunc("GET /api/quality", s.quality)
	api.HandleFunc("GET /api/speedtest", s.speedGet)
	api.HandleFunc("POST /api/speedtest", s.speedStart)
	api.HandleFunc("GET /api/speedtest/servers", s.speedServers)
	api.HandleFunc("POST /api/speedtest/stop", func(w http.ResponseWriter, r *http.Request) {
		s.core.Speed.Stop()
		writeJSON(w, map[string]bool{"ok": true})
	})
	api.HandleFunc("GET /api/speedtest/history", s.speedHistory)

	s.phase2Routes(api)
	s.phase3Routes(api)
	api.HandleFunc("GET /api/dns", s.dnsGet)
	api.HandleFunc("POST /api/dns", s.dnsSet)

	root := http.NewServeMux()
	root.HandleFunc("POST /api/login", s.handleLogin)
	root.HandleFunc("POST /api/logout", s.handleLogout)
	root.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "version": s.Version, "lang": s.core.Store.Get().Lang, "noPassword": rootHasNoPassword()})
	})
	root.Handle("/api/", s.requireAuth(api))
	root.Handle("/", s.static())
	return securityHeaders(root)
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		h.ServeHTTP(w, r)
	})
}

// static serves the SPA, falling back to index.html for client routes.
func (s *Server) static() http.Handler {
	fileServer := http.FileServer(http.FS(s.web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(s.web, p); err != nil {
			r.URL.Path = "/"
			p = "index.html"
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return false
	}
	return true
}

func respond(w http.ResponseWriter, err error) {
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- overview & system ----

type deviceView struct {
	devices.Device
	Name     string `json:"name"`
	Type     string `json:"type"`
	Blocked  bool   `json:"blocked"`
	Direct   bool   `json:"direct"`
	Priority bool   `json:"priority"`
	DownKbps int    `json:"downKbps"`
	UpKbps   int    `json:"upKbps"`
}

func (s *Server) deviceViews() []deviceView {
	cfg := s.core.Store.Get()
	list := s.core.Devices.List()
	out := make([]deviceView, 0, len(list))
	for _, d := range list {
		c := cfg.Devices[d.MAC]
		v := deviceView{Device: d, Name: c.Name, Type: c.Type, Blocked: c.Blocked, Direct: c.Direct,
			Priority: c.Priority, DownKbps: c.DownKbps, UpKbps: c.UpKbps}
		if v.Name == "" {
			v.Name = d.Hostname
		}
		if v.Type == "" {
			v.Type = d.GuessType
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	devs := s.deviceViews()
	online := 0
	for _, d := range devs {
		if d.Online {
			online++
		}
	}
	top := append([]deviceView{}, devs...)
	sort.Slice(top, func(i, j int) bool { return top[i].DownBps+top[i].UpBps > top[j].DownBps+top[j].UpBps })
	if len(top) > 4 {
		top = top[:4]
	}
	q := s.core.Quality.Report(10 * time.Minute)
	cfg := s.core.Store.Get()
	line, exit := s.core.PublicInfo()
	writeJSON(w, map[string]any{
		"system":   s.core.Sampler.Stats(),
		"wan":      s.core.Sampler.WAN(),
		"proxy":    s.core.Proxy.Status(),
		"split":    cfg.Proxy.SplitIran,
		"iranMode": cfg.Proxy.IranMode,
		"devices":  map[string]int{"total": len(devs), "online": online},
		"top":      top,
		"quality":  map[string]any{"ext": q.LastExt, "dom": q.LastDom},
		"operator": line,
		"exit":     exit,
		"fwError":  s.core.LastError(),
		"clock":    map[string]int64{"unixMs": time.Now().UnixMilli(), "offsetMin": int64(system.TZOffsetMinutes())},
	})
}

func (s *Server) system(w http.ResponseWriter, r *http.Request) {
	v := proxy.XrayVersion
	writeJSON(w, map[string]any{"system": s.core.Sampler.Stats(), "wan": s.core.Sampler.WAN(), "version": s.Version,
		"xray": fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])})
}

func (s *Server) reboot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"ok": true})
	if env.Mock {
		return
	}
	go func() {
		time.Sleep(time.Second)
		env.Run(10*time.Second, "reboot")
	}()
}

func (s *Server) setPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !checkPassword(in.Current) {
		writeErr(w, http.StatusForbidden, "current password is wrong")
		return
	}
	if len(in.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if env.Mock {
		respond(w, nil)
		return
	}
	_, err := env.RunInput(10*time.Second, in.Password+"\n"+in.Password+"\n", "passwd", "root")
	respond(w, err)
}

func (s *Server) backup(w http.ResponseWriter, r *http.Request) {
	if env.Mock {
		writeErr(w, http.StatusNotImplemented, "not available in demo mode")
		return
	}
	tmp := "/tmp/toofanwrt-backup.tar.gz"
	if _, err := env.Run(60*time.Second, "sysupgrade", "-b", tmp); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.Remove(tmp)
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", "attachment; filename=\"backup-"+time.Now().Format("2006-01-02")+".tar.gz\"")
	http.ServeFile(w, r, tmp)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.core.Proxy.Status().Events)
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Lang string `json:"lang"`
	}
	if !decode(w, r, &in) {
		return
	}
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		if in.Lang == "fa" || in.Lang == "en" {
			c.Lang = in.Lang
		}
		return nil
	}))
}

// ---- proxy ----

func (s *Server) proxyGet(w http.ResponseWriter, r *http.Request) {
	cfg := s.core.Store.Get().Proxy
	// Links contain credentials; the UI only needs the summary.
	for i := range cfg.Servers {
		cfg.Servers[i].Link = ""
	}
	for i := range cfg.OpenVPN {
		cfg.OpenVPN[i].Password, cfg.OpenVPN[i].Config = "", ""
	}
	writeJSON(w, map[string]any{"config": cfg, "status": s.core.Proxy.Status()})
}

func (s *Server) proxySettings(w http.ResponseWriter, r *http.Request) {
	var in map[string]json.RawMessage
	if !decode(w, r, &in) {
		return
	}
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		p := &c.Proxy
		boolField := func(k string, dst *bool) {
			if v, ok := in[k]; ok {
				json.Unmarshal(v, dst)
			}
		}
		boolField("enabled", &p.Enabled)
		if _, ok := in["splitIran"]; ok {
			boolField("splitIran", &p.SplitIran)
			p.IranMode = config.IranTunnel
			if p.SplitIran {
				p.IranMode = config.IranDirect
			}
		}
		if v, ok := in["iranMode"]; ok {
			var m string
			json.Unmarshal(v, &m)
			switch m {
			case config.IranDirect, config.IranTunnel, config.IranBlock:
				p.IranMode = m
				p.SplitIran = m == config.IranDirect
			default:
				return fmt.Errorf("unknown Iran mode %q", m)
			}
		}
		boolField("killSwitch", &p.KillSwitch)
		boolField("blockIPv6", &p.BlockIPv6)
		boolField("guestProxy", &p.GuestProxy)
		boolField("autoBest", &p.AutoBest)
		boolField("subAutoUpdate", &p.SubAutoUpdate)
		if v, ok := in["healthCheck"]; ok {
			var on bool
			json.Unmarshal(v, &on)
			p.HealthPaused = !on
		}
		if v, ok := in["healthInterval"]; ok {
			var sec int
			if json.Unmarshal(v, &sec) != nil || sec < config.MinHealthInterval || sec > config.MaxHealthInterval {
				return fmt.Errorf("health check interval must be %d–%d seconds", config.MinHealthInterval, config.MaxHealthInterval)
			}
			p.HealthInterval = sec
		}
		return nil
	}))
}

func (s *Server) proxyAddServers(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	text := strings.TrimSpace(in.Text)
	// A pasted subscription URL becomes a subscription.
	if strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://") {
		var subs []string
		for _, l := range strings.Fields(text) {
			if strings.HasPrefix(l, "http") {
				subs = append(subs, l)
			}
		}
		added := 0
		for _, u := range subs {
			if s.addSub("", u) == nil {
				added++
			}
		}
		go s.core.Proxy.UpdateSubscriptions("")
		writeJSON(w, map[string]any{"ok": true, "subscriptions": added})
		return
	}
	servers, bad := proxy.ParseLinks(text)
	if len(servers) == 0 {
		msg := "no valid link found"
		if len(bad) > 0 {
			msg = bad[0]
		}
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	added := 0
	err := s.core.Store.Update(func(c *config.Config) error {
		for _, sv := range servers {
			dup := false
			for _, e := range c.Proxy.Servers {
				if e.ID == sv.ID {
					dup = true
				}
			}
			if !dup {
				c.Proxy.Servers = append(c.Proxy.Servers, sv)
				added++
			}
		}
		if c.Proxy.ActiveID == "" && len(c.Proxy.Servers) > 0 {
			c.Proxy.ActiveID = c.Proxy.Servers[0].ID
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "added": added, "bad": bad})
}

func (s *Server) proxyAddOpenVPN(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		Config   string `json:"config"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	prof, srv, err := proxy.ImportOpenVPN(in.Config, strings.TrimSpace(in.Name), in.Username, in.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		for i, p := range c.Proxy.OpenVPN {
			if p.ID == prof.ID {
				c.Proxy.OpenVPN[i] = prof
				return nil
			}
		}
		c.Proxy.OpenVPN = append(c.Proxy.OpenVPN, prof)
		c.Proxy.Servers = append(c.Proxy.Servers, srv)
		if c.Proxy.ActiveID == "" {
			c.Proxy.ActiveID = srv.ID
		}
		return nil
	}))
}

func (s *Server) proxyDeleteServer(w http.ResponseWriter, r *http.Request) {
	s.deleteServers(w, []string{r.PathValue("id")})
}

func (s *Server) proxyDeleteServers(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs    []string `json:"ids"`
		Failed bool     `json:"failed"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Failed {
		for _, sv := range s.core.Store.Get().Proxy.Servers {
			if sv.LatencyMs < 0 && sv.SubID == "" {
				in.IDs = append(in.IDs, sv.ID)
			}
		}
	}
	s.deleteServers(w, in.IDs)
}

func (s *Server) deleteServers(w http.ResponseWriter, ids []string) {
	del := map[string]bool{}
	for _, id := range ids {
		del[id] = true
	}
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		kept := c.Proxy.Servers[:0]
		for _, sv := range c.Proxy.Servers {
			if !del[sv.ID] {
				kept = append(kept, sv)
			}
		}
		c.Proxy.Servers = kept
		profs := c.Proxy.OpenVPN[:0]
		for _, p := range c.Proxy.OpenVPN {
			if !del[p.ID] {
				profs = append(profs, p)
			}
		}
		c.Proxy.OpenVPN = profs
		if del[c.Proxy.PreferredID] {
			c.Proxy.PreferredID = ""
		}
		return nil
	}))
}

func (s *Server) proxySelect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &in) {
		return
	}
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		for _, sv := range c.Proxy.Servers {
			if sv.ID == in.ID {
				c.Proxy.ActiveID, c.Proxy.PreferredID = in.ID, in.ID
				return nil
			}
		}
		if in.ID == "" { // automatic: no preference
			c.Proxy.PreferredID = ""
			return nil
		}
		return errors.New("server not found")
	}))
}

func (s *Server) proxyTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []string `json:"ids"`
	}
	decode(w, r, &in)
	ids := in.IDs
	if len(ids) == 0 {
		ids = nil
	}
	// Results stream into the server list; the panel polls /proxy.
	writeJSON(w, map[string]any{"started": s.core.Proxy.StartTest(ids)})
}

func (s *Server) addSub(name, u string) error {
	id := proxy.SubID(u)
	return s.core.Store.Update(func(c *config.Config) error {
		for _, sub := range c.Proxy.Subscriptions {
			if sub.ID == id {
				return errors.New("subscription already added")
			}
		}
		if name == "" {
			name = hostOf(u)
		}
		c.Proxy.Subscriptions = append(c.Proxy.Subscriptions, config.Subscription{ID: id, Name: name, URL: u})
		return nil
	})
}

func hostOf(u string) string {
	re := regexp.MustCompile(`^https?://([^/:]+)`)
	if m := re.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return u
}

func (s *Server) proxyAddSub(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.URL = strings.TrimSpace(in.URL)
	if !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
		writeErr(w, http.StatusBadRequest, "subscription must be an http(s) URL")
		return
	}
	if err := s.addSub(in.Name, in.URL); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	err := s.core.Proxy.UpdateSubscriptions(proxy.SubID(in.URL))
	if err != nil {
		writeJSON(w, map[string]any{"ok": true, "warning": err.Error()})
		return
	}
	s.core.Store.Update(func(c *config.Config) error {
		if c.Proxy.ActiveID == "" && len(c.Proxy.Servers) > 0 {
			c.Proxy.ActiveID = c.Proxy.Servers[0].ID
		}
		return nil
	})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) proxyUpdateSub(w http.ResponseWriter, r *http.Request) {
	respond(w, s.core.Proxy.UpdateSubscriptions(r.PathValue("id")))
}

func (s *Server) proxyDeleteSub(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		subs := c.Proxy.Subscriptions[:0]
		for _, sub := range c.Proxy.Subscriptions {
			if sub.ID != id {
				subs = append(subs, sub)
			}
		}
		c.Proxy.Subscriptions = subs
		kept := c.Proxy.Servers[:0]
		for _, sv := range c.Proxy.Servers {
			if sv.SubID != id {
				kept = append(kept, sv)
			}
		}
		c.Proxy.Servers = kept
		return nil
	}))
}

func (s *Server) proxyLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(s.core.Proxy.Logs()))
}

// ---- devices ----

var macRe = regexp.MustCompile(`^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)

// deviceInfo is the detail view: usage history, reservation, connections.
func (s *Server) deviceInfo(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(r.PathValue("mac"))
	if !macRe.MatchString(mac) {
		writeErr(w, http.StatusBadRequest, "invalid MAC")
		return
	}
	days := s.core.Devices.Usage(mac, 30)
	md, mu := s.core.Devices.MonthUsage(mac)
	ip := s.core.Devices.IPs()[mac]
	writeJSON(w, map[string]any{
		"days":        days,
		"today":       days[len(days)-1],
		"month":       map[string]uint64{"down": md, "up": mu},
		"staticIp":    devices.StaticIP(mac),
		"connections": devices.Connections(ip),
		"sites":       len(s.core.Sites.Sites(mac)),
		"firstSeen":   s.core.Store.Get().Devices[mac].FirstSeen,
	})
}

func (s *Server) deviceWake(w http.ResponseWriter, r *http.Request) {
	respond(w, devices.Wake(strings.ToLower(r.PathValue("mac"))))
}

func (s *Server) sitesGet(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(r.URL.Query().Get("mac"))
	writeJSON(w, map[string]any{
		"enabled": s.core.Store.Get().DNS.LogSites,
		"since":   s.core.Sites.Since(),
		"sites":   s.core.Sites.Sites(mac),
		"counts":  s.core.Sites.Counts(),
	})
}

func (s *Server) sitesSet(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	// Start recording now; dnsmasq follows with the (debounced) apply.
	s.core.Sites.SetEnabled(in.Enabled)
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		c.DNS.LogSites = in.Enabled
		return nil
	}))
}

func (s *Server) sitesClear(w http.ResponseWriter, r *http.Request) {
	s.core.Sites.Clear(strings.ToLower(r.URL.Query().Get("mac")))
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) devicesGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.deviceViews())
}

func (s *Server) deviceSet(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(r.PathValue("mac"))
	if !macRe.MatchString(mac) {
		writeErr(w, http.StatusBadRequest, "invalid MAC")
		return
	}
	var in map[string]json.RawMessage
	if !decode(w, r, &in) {
		return
	}
	// The address reservation lives in OpenWrt's DHCP config, not ours.
	if v, ok := in["staticIp"]; ok {
		var ip, name string
		json.Unmarshal(v, &ip)
		json.Unmarshal(in["name"], &name)
		if err := devices.SetStaticIP(mac, strings.TrimSpace(ip), name); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		d := c.Devices[mac]
		if d.FirstSeen == 0 {
			d.FirstSeen = time.Now().Unix()
		}
		for k, v := range in {
			var err error
			switch k {
			case "name":
				err = json.Unmarshal(v, &d.Name)
				if len(d.Name) > 64 {
					d.Name = d.Name[:64]
				}
			case "type":
				err = json.Unmarshal(v, &d.Type)
			case "blocked":
				err = json.Unmarshal(v, &d.Blocked)
			case "direct":
				err = json.Unmarshal(v, &d.Direct)
			case "priority":
				err = json.Unmarshal(v, &d.Priority)
			case "downKbps":
				err = json.Unmarshal(v, &d.DownKbps)
			case "upKbps":
				err = json.Unmarshal(v, &d.UpKbps)
			}
			if err != nil {
				return err
			}
		}
		if d.DownKbps < 0 || d.UpKbps < 0 {
			return errors.New("invalid limit")
		}
		c.Devices[mac] = d
		return nil
	}))
}

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

func (s *Server) schedulesGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.core.Store.Get().Schedules)
}

func (s *Server) scheduleSave(w http.ResponseWriter, r *http.Request) {
	var in config.Schedule
	if !decode(w, r, &in) {
		return
	}
	if !hhmm.MatchString(in.From) || !hhmm.MatchString(in.To) {
		writeErr(w, http.StatusBadRequest, "time must be HH:MM")
		return
	}
	for i, m := range in.MACs {
		in.MACs[i] = strings.ToLower(m)
		if !macRe.MatchString(in.MACs[i]) {
			writeErr(w, http.StatusBadRequest, "invalid MAC")
			return
		}
	}
	for _, d := range in.Days {
		if d < 0 || d > 6 {
			writeErr(w, http.StatusBadRequest, "invalid day")
			return
		}
	}
	if in.ID == "" {
		in.ID = proxy.SubID(time.Now().String())
	}
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		for i := range c.Schedules {
			if c.Schedules[i].ID == in.ID {
				c.Schedules[i] = in
				return nil
			}
		}
		c.Schedules = append(c.Schedules, in)
		return nil
	}))
}

func (s *Server) scheduleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		kept := c.Schedules[:0]
		for _, sc := range c.Schedules {
			if sc.ID != id {
				kept = append(kept, sc)
			}
		}
		c.Schedules = kept
		return nil
	}))
}

// ---- wifi ----

func (s *Server) wifiGet(w http.ResponseWriter, r *http.Request) {
	st, err := wifi.Get()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"bands": st.Bands, "guest": st.Guest, "country": st.Country, "smartWifi": s.core.Store.Get().SmartWiFi})
}

func (s *Server) wifiSetBand(w http.ResponseWriter, r *http.Request) {
	var in wifi.Band
	if !decode(w, r, &in) {
		return
	}
	err := wifi.SetBand(in)
	if err == nil {
		s.core.Reapply() // Wi-Fi device names may change after a reload
	}
	respond(w, err)
}

func (s *Server) wifiSetGuest(w http.ResponseWriter, r *http.Request) {
	var in wifi.Guest
	if !decode(w, r, &in) {
		return
	}
	err := wifi.SetGuest(in)
	if err == nil {
		s.core.RequestApply()
	}
	respond(w, err)
}

// ---- quality / speed ----

func (s *Server) quality(w http.ResponseWriter, r *http.Request) {
	win := 24 * time.Hour
	if r.URL.Query().Get("window") == "7d" {
		win = 7 * 24 * time.Hour
	}
	writeJSON(w, s.core.Quality.Report(win))
}

func (s *Server) speedGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.core.Speed.Get())
}

func (s *Server) speedStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Via    string           `json:"via"`
		Server core.SpeedServer `json:"server"`
	}
	decode(w, r, &in)
	tunnel := in.Via == "tunnel" && s.core.Proxy.Mode() == proxy.ModeConnected
	if in.Via == "tunnel" && !tunnel {
		writeErr(w, http.StatusBadRequest, "the tunnel is not connected")
		return
	}
	if !s.core.Speed.Start(tunnel, in.Server) {
		writeErr(w, http.StatusConflict, "a test is already running")
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) speedServers(w http.ResponseWriter, r *http.Request) {
	tunnel := r.URL.Query().Get("via") == "tunnel" && s.core.Proxy.Mode() == proxy.ModeConnected
	list, err := s.core.Speed.Servers(tunnel, r.URL.Query().Get("q"))
	resp := map[string]any{"servers": list}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, resp)
}

func (s *Server) speedHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.core.Speed.History())
}

// ---- dns ----

func (s *Server) dnsGet(w http.ResponseWriter, r *http.Request) {
	cfg := s.core.Store.Get()
	writeJSON(w, map[string]any{
		"config":    cfg.DNS,
		"providers": proxy.Providers,
		"tunnel":    s.core.Proxy.UseTunnelDNS(),
		"isp":       s.core.Sampler.WAN().DNS,
		"lockWan":   cfg.LockWAN,
	})
}

func (s *Server) dnsSet(w http.ResponseWriter, r *http.Request) {
	var in config.DNS
	if !decode(w, r, &in) {
		return
	}
	if in.Provider != "custom" && proxy.FindProvider(in.Provider) == nil {
		writeErr(w, http.StatusBadRequest, "unknown provider")
		return
	}
	if in.Provider == "custom" {
		list := strings.Fields(strings.ReplaceAll(in.Custom, ",", " "))
		if len(list) == 0 || len(list) > 2 {
			writeErr(w, http.StatusBadRequest, "enter one or two DNS servers")
			return
		}
		for _, d := range list {
			if net.ParseIP(d) == nil && !strings.HasPrefix(d, "https://") {
				writeErr(w, http.StatusBadRequest, "DNS must be an IP address or an https:// DoH URL")
				return
			}
		}
		in.Custom = strings.Join(list, " ")
	}
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		c.DNS = in
		return nil
	}))
}
