package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"separ/internal/config"
	"separ/internal/env"
	"separ/internal/luci"
	"separ/internal/netcfg"
	"separ/internal/proxy"
	"separ/internal/update"
	"separ/internal/wifi"
)

func (s *Server) phase3Routes(api *http.ServeMux) {
	api.HandleFunc("GET /api/update", s.updateGet)
	api.HandleFunc("POST /api/update/check", s.updateCheck)
	api.HandleFunc("POST /api/update/install", s.updateInstall)
	api.HandleFunc("POST /api/update/upload", s.updateUpload)
	api.HandleFunc("POST /api/update/source", s.updateSource)
	api.HandleFunc("POST /api/system/restore", s.restore)
	api.HandleFunc("GET /api/system/export", s.exportSettings)
	api.HandleFunc("POST /api/system/import", s.importSettings)
	api.HandleFunc("GET /api/luci", s.luciGet)
	api.HandleFunc("POST /api/luci", s.luciSet)
	api.HandleFunc("GET /api/network", s.netGet)
	api.HandleFunc("POST /api/network/wan", s.netWAN)
	api.HandleFunc("POST /api/network/lan", s.netLAN)
	api.HandleFunc("POST /api/network/system", s.netSystem)
	api.HandleFunc("GET /api/network/uplink", s.uplinkGet)
	api.HandleFunc("POST /api/network/uplink/scan", s.uplinkScan)
	api.HandleFunc("POST /api/network/uplink", s.uplinkConnect)
	api.HandleFunc("POST /api/network/uplink/disconnect", s.uplinkDisconnect)
}

func (s *Server) updater() *update.Updater {
	s.updOnce.Do(func() {
		port := 80
		if len(s.core.PanelPorts) > 0 {
			port = s.core.PanelPorts[0]
		}
		s.upd = &update.Updater{Version: s.Version, Port: port, SocksPort: proxy.SocksPort, Simulate: env.Mock}
	})
	return s.upd
}

func (s *Server) updateGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"status": s.updater().Status(), "source": s.core.Store.Get().UpdateURL})
}

func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	m, err := s.updater().Check(s.core.Store.Get().UpdateURL)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"manifest": m, "newer": m.Version != s.Version})
}

func (s *Server) updateInstall(w http.ResponseWriter, r *http.Request) {
	u := s.updater()
	if u.Busy() {
		writeErr(w, http.StatusConflict, "an update is already running")
		return
	}
	// The manifest is fetched again here rather than trusted from the browser.
	m, err := u.Check(s.core.Store.Get().UpdateURL)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	go u.Install(m)
	writeJSON(w, map[string]any{"ok": true, "version": m.Version})
}

// saveUpload stores a raw request body (max 128 MB) in a temp file.
func saveUpload(w http.ResponseWriter, r *http.Request, name string) (string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 128<<20)
	dir := "/tmp/separ-update"
	if env.Mock {
		dir = filepath.Join(os.TempDir(), "separ-update")
	}
	os.MkdirAll(dir, 0o700)
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, r.Body)
	f.Close()
	if err == nil && n < 1024 {
		err = errors.New("file is empty or too small")
	}
	if err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func (s *Server) updateUpload(w http.ResponseWriter, r *http.Request) {
	u := s.updater()
	if u.Busy() {
		writeErr(w, http.StatusConflict, "an update is already running")
		return
	}
	path, err := saveUpload(w, r, "release.tar.gz")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := u.Apply(path); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "status": u.Status()})
}

func (s *Server) updateSource(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.URL = strings.TrimSpace(in.URL)
	if in.URL != "" && !strings.HasPrefix(in.URL, "https://") && !strings.HasPrefix(in.URL, "http://") {
		writeErr(w, http.StatusBadRequest, "must be an http(s) URL")
		return
	}
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		c.UpdateURL = in.URL
		return nil
	}))
}

// restore applies a sysupgrade backup (all router settings) and reboots.
func (s *Server) restore(w http.ResponseWriter, r *http.Request) {
	path, err := saveUpload(w, r, "restore.tar.gz")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if env.Mock {
		os.Remove(path)
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	list, err := env.Run(30*time.Second, "tar", "-tzf", path)
	if err != nil || !strings.Contains(list, "etc/config/") {
		os.Remove(path)
		writeErr(w, http.StatusBadRequest, "not a router backup file")
		return
	}
	if _, err := env.Run(60*time.Second, "sysupgrade", "-r", path); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
	go func() {
		time.Sleep(2 * time.Second)
		env.Run(10*time.Second, "reboot")
	}()
}

// exportSettings downloads the panel's own settings (servers, devices,
// schedules...). It contains server credentials, like the router backup.
func (s *Server) exportSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\"separ-settings-"+time.Now().Format("2006-01-02")+".json\"")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(s.core.Store.Get())
}

func (s *Server) importSettings(w http.ResponseWriter, r *http.Request) {
	in := config.Default()
	if !decode(w, r, in) {
		return
	}
	if in.Devices == nil {
		in.Devices = map[string]config.Device{}
	}
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		*c = *in
		return nil
	}))
}

func (s *Server) luciGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, luci.Get())
}

func (s *Server) luciSet(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := luci.Set(in.Enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, luci.Get())
}

func (s *Server) netGet(w http.ResponseWriter, r *http.Request) {
	st, err := netcfg.Get()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	st.WAN.Password = ""
	writeJSON(w, st)
}

func (s *Server) netWAN(w http.ResponseWriter, r *http.Request) {
	var in netcfg.WAN
	if !decode(w, r, &in) {
		return
	}
	respond(w, netcfg.SetWAN(in))
}

func (s *Server) netLAN(w http.ResponseWriter, r *http.Request) {
	var in netcfg.LAN
	if !decode(w, r, &in) {
		return
	}
	respond(w, netcfg.SetLAN(in))
}

func (s *Server) netSystem(w http.ResponseWriter, r *http.Request) {
	var in netcfg.System
	if !decode(w, r, &in) {
		return
	}
	respond(w, netcfg.SetSystem(in))
}

func (s *Server) uplinkGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, wifi.UplinkStatus())
}

func (s *Server) uplinkScan(w http.ResponseWriter, r *http.Request) {
	nets, err := wifi.ScanNetworks()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, nets)
}

func (s *Server) uplinkConnect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		wifi.Network
		Key string `json:"key"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := wifi.ConnectUplink(in.Network, in.Key); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.core.Reapply()
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) uplinkDisconnect(w http.ResponseWriter, r *http.Request) {
	respond(w, wifi.DisconnectUplink())
}
