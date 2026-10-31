package xraycore

import (
	"archive/zip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestParseVersion(t *testing.T) {
	for in, want := range map[string][3]int{
		"v26.10.29": {26, 10, 29},
		"Xray 26.3.27 (Xray, Penetrates Everything.) OpenWrt": {26, 3, 27},
		"24.12.31": {24, 12, 31},
	} {
		if got, ok := ParseVersion(in); !ok || got != want {
			t.Errorf("ParseVersion(%q) = %v %v", in, got, ok)
		}
	}
	if !less([3]int{24, 12, 30}, MinVersion) || less(MinVersion, MinVersion) || less([3]int{26, 1, 1}, MinVersion) {
		t.Error("version ordering")
	}
}

func TestExtract(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "x.zip")
	f, _ := os.Create(zp)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("geoip.dat")
	w.Write([]byte("geo"))
	w, _ = zw.Create("xray")
	w.Write([]byte("#!/bin/sh\necho Xray 26.10.29\n"))
	zw.Close()
	f.Close()
	dst := filepath.Join(dir, "xray")
	if err := extract(zp, "xray", dst); err != nil {
		t.Fatal(err)
	}
	if v := Version(dst); v != "26.10.29" {
		t.Errorf("version of extracted binary = %q", v)
	}
	if err := extract(zp, "missing", dst+"2"); err == nil {
		t.Error("missing file not reported")
	}
}

func TestReleaseSources(t *testing.T) {
	api := `[{"tag_name":"v26.10.29","published_at":"2026-10-29T10:00:00Z","prerelease":false,"html_url":"u","assets":[{"name":"` + Asset() + `"}]},
	{"tag_name":"v26.10.30-beta","published_at":"2026-10-30T10:00:00Z","prerelease":true,"assets":[{"name":"` + Asset() + `"}]},
	{"tag_name":"v24.11.30","published_at":"2024-11-30T10:00:00Z","assets":[{"name":"` + Asset() + `"}]},
	{"tag_name":"v26.9.9","published_at":"2026-09-09T10:00:00Z","assets":[{"name":"Xray-windows-64.zip"}]}]`
	atom := `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom">
	<entry><updated>2026-10-29T10:00:00Z</updated><link rel="alternate" href="https://github.com/XTLS/Xray-core/releases/tag/v26.10.29"/></entry>
	<entry><updated>2024-11-30T10:00:00Z</updated><link rel="alternate" href="https://github.com/XTLS/Xray-core/releases/tag/v24.11.30"/></entry></feed>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api":
			w.Write([]byte(api))
		case "/atom":
			w.Write([]byte(atom))
		default:
			http.Error(w, "rate limited", http.StatusForbidden)
		}
	}))
	defer srv.Close()
	releasesURL, atomURL = srv.URL+"/api", srv.URL+"/atom"
	relCache = nil
	list, err := Releases()
	if err != nil || len(list) != 2 || list[0].Version != "26.10.29" || !list[1].Prerelease {
		t.Fatalf("api: %+v %v", list, err)
	}
	releasesURL, relCache = srv.URL+"/blocked", nil
	list, err = Releases()
	if err != nil || len(list) != 1 || list[0].Tag != "v26.10.29" {
		t.Fatalf("atom fallback: %+v %v", list, err)
	}
}
