package api

import (
	"net/http"
	"regexp"

	"toofanwrt/internal/firmware"
)

// OpenWrt firmware upgrade (System page).

func (s *Server) registerFirmware(api *http.ServeMux) {
	api.HandleFunc("GET /api/firmware", s.firmwareGet)
	api.HandleFunc("GET /api/firmware/job", s.firmwareJob)
	api.HandleFunc("POST /api/firmware/prepare", s.firmwarePrepare)
	api.HandleFunc("POST /api/firmware/flash", s.firmwareFlash)
	api.HandleFunc("POST /api/firmware/cancel", s.firmwareCancel)
	api.HandleFunc("POST /api/firmware/repair", s.firmwareRepair)
}

var fwVerRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func (s *Server) firmwareGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, firmware.Get())
}

func (s *Server) firmwareJob(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"job": firmware.CurrentJob(), "heal": firmware.HealStatus()})
}

func (s *Server) firmwarePrepare(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version string `json:"version"`
		Custom  bool   `json:"custom"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !fwVerRe.MatchString(in.Version) {
		writeErr(w, http.StatusBadRequest, "invalid version")
		return
	}
	if err := firmware.Start(in.Version, in.Custom); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, firmware.CurrentJob())
}

func (s *Server) firmwareFlash(w http.ResponseWriter, r *http.Request) {
	if s.updater().Busy() {
		writeErr(w, http.StatusConflict, "a panel update is running")
		return
	}
	if err := firmware.Flash(); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, firmware.CurrentJob())
}

func (s *Server) firmwareCancel(w http.ResponseWriter, r *http.Request) {
	respond(w, firmware.Cancel())
}

func (s *Server) firmwareRepair(w http.ResponseWriter, r *http.Request) {
	go firmware.Repair()
	writeJSON(w, firmware.HealStatus())
}
