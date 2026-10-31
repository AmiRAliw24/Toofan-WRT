package api

import (
	"net/http"
	"regexp"

	"toofanwrt/internal/config"
	"toofanwrt/internal/geo"
	"toofanwrt/internal/xraycore"
)

// Geo data and Xray cores: what 3x-ui calls "Xray version" and "geofiles".

func (s *Server) registerCores(api *http.ServeMux) {
	api.HandleFunc("GET /api/geo", s.geoGet)
	api.HandleFunc("POST /api/geo/update", s.geoUpdate)
	api.HandleFunc("POST /api/geo/rollback", s.geoRollback)
	api.HandleFunc("POST /api/geo/settings", s.geoSettings)
	api.HandleFunc("GET /api/xray", s.xrayGet)
	api.HandleFunc("GET /api/xray/releases", s.xrayReleases)
	api.HandleFunc("POST /api/xray/install", s.xrayInstall)
	api.HandleFunc("POST /api/xray/rollback", s.xrayRollback)
}

func (s *Server) xrayGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, xraycore.Describe(s.core.Proxy.EngineBin(), s.core.Proxy.XrayCompatBin))
}

func (s *Server) xrayReleases(w http.ResponseWriter, r *http.Request) {
	list, err := xraycore.Releases()
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, list)
}

func (s *Server) xrayInstall(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tag string `json:"tag"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !tagRe.MatchString(in.Tag) {
		writeErr(w, http.StatusBadRequest, "invalid version")
		return
	}
	if err := xraycore.Start(in.Tag, s.core.Proxy.EngineBin(), s.core.Proxy.SetEngine); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, xraycore.CurrentJob())
}

var tagRe = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

func (s *Server) xrayRollback(w http.ResponseWriter, r *http.Request) {
	if err := xraycore.Rollback(s.core.Proxy.SetEngine); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, xraycore.Describe(s.core.Proxy.EngineBin(), s.core.Proxy.XrayCompatBin))
}

func (s *Server) geoGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"source":     geo.Source,
		"sourceUrl":  "https://github.com/" + geo.Source,
		"downloaded": geo.Downloaded(),
		"meta":       geo.LoadMeta(),
		"job":        geo.CurrentJob(),
		"autoUpdate": s.core.Store.Get().Geo.AutoUpdate,
		"assetDir":   geo.AssetDir(),
	})
}

// geoUpdate starts a download in the background; the panel polls GET.
func (s *Server) geoUpdate(w http.ResponseWriter, r *http.Request) {
	if geo.CurrentJob().Running {
		writeErr(w, http.StatusConflict, "an update is already running")
		return
	}
	go s.core.UpdateGeo()
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) geoRollback(w http.ResponseWriter, r *http.Request) {
	if err := geo.Rollback(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.core.ReloadGeo()
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) geoSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AutoUpdate bool `json:"autoUpdate"`
	}
	if !decode(w, r, &in) {
		return
	}
	respond(w, s.core.Store.Update(func(c *config.Config) error {
		c.Geo.AutoUpdate = in.AutoUpdate
		return nil
	}))
}
