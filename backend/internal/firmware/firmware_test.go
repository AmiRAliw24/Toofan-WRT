package firmware

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var list = []string{"25.12.6", "25.12.0-rc1", "25.12.0", "24.10.8", "24.10.0", "23.05.6", "23.05.4", "23.05.0", "22.03.7"}

func find(opts []Option, v string) *Option {
	for i := range opts {
		if opts[i].Version == v {
			return &opts[i]
		}
	}
	return nil
}

func TestOptionsOneSeriesAtATime(t *testing.T) {
	opts := Options("23.05.4", list, "25.12.6")
	if len(opts) != 3 {
		t.Fatalf("want 23.05, 24.10, 25.12 latest only, got %+v", opts)
	}
	if o := find(opts, "23.05.6"); o == nil || !o.Allowed || o.Changes {
		t.Fatalf("23.05.6 (same series) must be allowed: %+v", o)
	}
	if o := find(opts, "24.10.8"); o == nil || !o.Allowed || !o.Changes || o.PM != "opkg" {
		t.Fatalf("24.10.8 (next series) must be allowed: %+v", o)
	}
	if o := find(opts, "25.12.6"); o == nil || o.Allowed || o.Reason != "step" || o.PM != "apk" || !o.Stable {
		t.Fatalf("25.12.6 must wait for 24.10 first: %+v", o)
	}
	if find(opts, "22.03.7") != nil || find(opts, "25.12.0-rc1") != nil {
		t.Fatal("older series and release candidates must not be listed")
	}

	opts = Options("23.05.6", list, "25.12.6")
	if o := find(opts, "23.05.6"); o == nil || !o.Current || o.Allowed {
		t.Fatalf("current release is not an upgrade: %+v", o)
	}
	opts = Options("24.10.8", list, "25.12.6")
	if o := find(opts, "25.12.6"); o == nil || !o.Allowed {
		t.Fatalf("from 24.10, 25.12 is next: %+v", o)
	}
	if find(opts, "23.05.6") != nil {
		t.Fatal("downgrades must not be listed")
	}
	// A snapshot / unknown current version: nothing is offered.
	for _, o := range Options("SNAPSHOT", list, "25.12.6") {
		if o.Allowed {
			t.Fatalf("nothing may be offered on a snapshot: %+v", o)
		}
	}
}

func TestFindImageByBoardName(t *testing.T) {
	var p profiles
	json.Unmarshal([]byte(`{"profiles":{
		"google_wifi":{"supported_devices":["google,wifi"],"images":[
			{"type":"factory","name":"f.bin","sha256":"`+strings.Repeat("a", 64)+`"},
			{"type":"sysupgrade","name":"s.bin","sha256":"`+strings.Repeat("b", 64)+`"}]},
		"other":{"supported_devices":["x,y"],"images":[{"type":"sysupgrade","name":"o.bin","sha256":"`+strings.Repeat("c", 64)+`"}]}}}`), &p)
	id, img, err := findImage(p, "https://d/", "google,wifi", "24.10.8")
	if err != nil || id != "google_wifi" || img.Name != "s.bin" || img.URL != "https://d/s.bin" {
		t.Fatalf("got %s %+v %v", id, img, err)
	}
	if _, _, err := findImage(p, "https://d/", "linksys,ea8300", "24.10.8"); err == nil {
		t.Fatal("an image for another board must never be picked")
	}
}

// fakeOpenWrt serves downloads.openwrt.org and the image builder.
type fakeOpenWrt struct {
	official, custom []byte
	badCustomSum     bool
	asuFails         int32 // build requests answered with an error
	builds           int32
	polls            int32
}

func (f *fakeOpenWrt) start(t *testing.T) {
	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/.versions.json":
			w.Write([]byte(`{"stable_version":"25.12.6","versions_list":["25.12.6","24.10.8","23.05.6","23.05.4"]}`))
		case r.URL.Path == "/releases/24.10.8/targets/ipq40xx/chromium/profiles.json":
			w.Write([]byte(`{"profiles":{"google_wifi":{"supported_devices":["google,wifi"],"images":[{"type":"sysupgrade","name":"official.bin","sha256":"` + sum(f.official) + `"}]}}}`))
		case r.URL.Path == "/releases/24.10.8/targets/ipq40xx/chromium/official.bin":
			w.Write(f.official)
		case r.URL.Path == "/api/v1/build" && r.Method == "POST":
			var req asuRequest
			json.NewDecoder(r.Body).Decode(&req)
			n := atomic.AddInt32(&f.builds, 1)
			if req.Profile != "google_wifi" || req.Target != "ipq40xx/chromium" {
				w.WriteHeader(400)
				w.Write([]byte(`{"detail":"bad profile"}`))
				return
			}
			if n <= atomic.LoadInt32(&f.asuFails) {
				w.WriteHeader(422)
				w.Write([]byte(`{"detail":"Error: Impossible package selection","stderr":"x\nmissing package foo"}`))
				return
			}
			w.WriteHeader(202)
			w.Write([]byte(`{"status":202,"detail":"queued","request_hash":"abc"}`))
		case r.URL.Path == "/api/v1/build/abc":
			if atomic.AddInt32(&f.polls, 1) < 2 {
				w.WriteHeader(202)
				w.Write([]byte(`{"status":202,"detail":"started","imagebuilder_status":"building_image","request_hash":"abc"}`))
				return
			}
			s := sum(f.custom)
			if f.badCustomSum {
				s = strings.Repeat("0", 64)
			}
			w.Write([]byte(`{"status":200,"bin_dir":"abc","images":[{"type":"factory","name":"f.bin","sha256":"` + strings.Repeat("1", 64) + `"},{"type":"sysupgrade","name":"custom.bin","sha256":"` + s + `"}]}`))
		case r.URL.Path == "/store/abc/custom.bin":
			w.Write(f.custom)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	downloadsURL, asuURL = srv.URL, srv.URL
}

func setup(t *testing.T) (tested *string) {
	dir := t.TempDir()
	WorkDir = filepath.Join(dir, "work")
	MarkerPath = filepath.Join(dir, "etc/toofanwrt/firmware.json")
	KeepPath = filepath.Join(dir, "keep.d/toofanwrt-firmware")
	RestorePath = filepath.Join(dir, "uci-defaults/99-toofanwrt-restore")
	os.MkdirAll(filepath.Dir(KeepPath), 0o755)
	boardFn = func() (Board, error) {
		return Board{BoardName: "google,wifi", Version: "23.05.4", Target: "ipq40xx/chromium", Compat: "1.1"}, nil
	}
	memAvailFn = func() int64 { return 400 << 20 }
	var s string
	tested = &s
	testFn = func(img string) (string, error) {
		b, _ := os.ReadFile(img)
		s = string(b)
		return "", nil
	}
	pollEvery = 10 * time.Millisecond
	verAt = time.Time{}
	job = Job{State: "idle"}
	t.Cleanup(func() { boardFn, memAvailFn, flashFn = readBoard, memAvailable, startSysupgrade })
	return tested
}

func wait(t *testing.T, states ...string) Job {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j := CurrentJob()
		for _, s := range states {
			if j.State == s {
				return j
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job stuck: %+v", CurrentJob())
	return Job{}
}

func TestPrepareCustomImage(t *testing.T) {
	tested := setup(t)
	f := &fakeOpenWrt{official: []byte("OFFICIAL"), custom: []byte("CUSTOM")}
	f.start(t)
	if err := Start("25.12.6", true); err == nil {
		t.Fatal("skipping a series must be refused")
	}
	if err := Start("24.10.8", true); err != nil {
		t.Fatal(err)
	}
	j := wait(t, "ready", "failed")
	if j.State != "ready" || !j.Custom || j.Image != "custom.bin" || *tested != "CUSTOM" {
		t.Fatalf("custom image not prepared: %+v (tested %q)", j, *tested)
	}
	if err := Start("24.10.8", true); err != nil {
		t.Fatal("a ready image may be replaced:", err)
	}
	wait(t, "ready", "failed")
}

func TestPrepareFallsBackToOfficial(t *testing.T) {
	tested := setup(t)
	f := &fakeOpenWrt{official: []byte("OFFICIAL"), custom: []byte("CUSTOM"), asuFails: 2}
	f.start(t)
	Start("24.10.8", true)
	j := wait(t, "ready", "failed")
	if j.State != "ready" || j.Custom || j.Image != "official.bin" || *tested != "OFFICIAL" || !strings.Contains(j.Fallback, "missing package foo") {
		t.Fatalf("fallback not used: %+v", j)
	}
	if f.builds != 2 {
		t.Fatalf("want a retry with only the required packages, got %d builds", f.builds)
	}
}

func TestPrepareRefusesDamagedImage(t *testing.T) {
	tested := setup(t)
	f := &fakeOpenWrt{official: []byte("OFFICIAL"), custom: []byte("CUSTOM"), badCustomSum: true}
	f.start(t)
	Start("24.10.8", true)
	j := wait(t, "ready", "failed")
	if j.State != "failed" || !strings.Contains(j.Error, "damaged") || *tested != "" {
		t.Fatalf("damaged image must never reach sysupgrade: %+v", j)
	}
	if _, err := os.Stat(imagePath()); err == nil {
		t.Fatal("damaged image left in RAM")
	}
	if err := Flash(); err == nil {
		t.Fatal("flash without a checked image")
	}
}

func TestPrepareStopsWhenSysupgradeRefuses(t *testing.T) {
	setup(t)
	testFn = func(string) (string, error) {
		return "Image version mismatch: image 1.1, device 1.0", errors.New("exit 1")
	}
	f := &fakeOpenWrt{official: []byte("OFFICIAL"), custom: []byte("CUSTOM")}
	f.start(t)
	Start("24.10.8", false)
	j := wait(t, "ready", "failed")
	if j.State != "failed" || !strings.Contains(j.Error, "mismatch") {
		t.Fatalf("got %+v", j)
	}
}

func TestPrepareNeedsMemory(t *testing.T) {
	setup(t)
	memAvailFn = func() int64 { return 60 << 20 }
	f := &fakeOpenWrt{official: []byte("OFFICIAL")}
	f.start(t)
	Start("24.10.8", false)
	if j := wait(t, "ready", "failed"); j.State != "failed" || !strings.Contains(j.Error, "memory") {
		t.Fatalf("got %+v", j)
	}
}

func TestFlashWritesRestoreFiles(t *testing.T) {
	setup(t)
	f := &fakeOpenWrt{official: []byte("OFFICIAL")}
	f.start(t)
	Start("24.10.8", false)
	wait(t, "ready")
	var flashed string
	flashFn = func(img string) error { flashed = img; return nil }
	LuciPresent = func() bool { return false }
	if err := Flash(); err != nil {
		t.Fatal(err)
	}
	if flashed != imagePath() || CurrentJob().State != "flashing" {
		t.Fatalf("flashed %q state %s", flashed, CurrentJob().State)
	}
	keep, _ := os.ReadFile(KeepPath)
	for _, want := range []string{"/usr/bin/toofanwrt\n", "/etc/rc.d/S99toofanwrt\n", "/usr/libexec/toofanwrt/\n", RestorePath + "\n", "/etc/toofanwrt/\n"} {
		if !strings.Contains(string(keep), want) {
			t.Fatalf("keep list lacks %q:\n%s", want, keep)
		}
	}
	m, err := ReadMarker()
	if err != nil || m.From != "23.05.4" || m.To != "24.10.8" || !m.RemoveLuci || m.Healed {
		t.Fatalf("marker %+v %v", m, err)
	}
	if err := Cancel(); err == nil {
		t.Fatal("cancel during flashing")
	}

	// sysupgrade refusing to start undoes everything.
	setup(t)
	f.start(t)
	Start("24.10.8", false)
	wait(t, "ready")
	flashFn = func(string) error { return errors.New("boom") }
	if err := Flash(); err == nil {
		t.Fatal("want error")
	}
	for _, p := range []string{KeepPath, RestorePath, MarkerPath} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s left behind after a failed start", p)
		}
	}
}

func TestScriptsParse(t *testing.T) {
	for name, s := range map[string]string{"restore": restoreScript, "heal": healScript()} {
		cmd := exec.Command("sh", "-n")
		cmd.Stdin = strings.NewReader(s)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s script: %v %s", name, err, out)
		}
	}
}

func TestRepairMarksMarker(t *testing.T) {
	setup(t)
	writeMarker(Marker{From: "23.05.4", To: "24.10.8", RemoveLuci: true})
	healRun = func() (string, error) { return "installed: kmod-nft-tproxy", nil }
	removed, healed := false, false
	RemoveLuci = func() error { removed = true; return nil }
	OnHealed = func() { healed = true }
	if err := Repair(); err != nil {
		t.Fatal(err)
	}
	m, _ := ReadMarker()
	if !m.Healed || !removed || !healed || HealStatus().State != "ok" {
		t.Fatalf("marker %+v removed %v healed %v", m, removed, healed)
	}
	// Later repairs don't remove LuCI again (the owner may have put it back).
	removed = false
	Repair()
	if removed {
		t.Fatal("LuCI removed twice")
	}
	healRun = func() (string, error) { return "", errors.New("no access to the package server") }
	if Repair() == nil || HealStatus().State != "failed" {
		t.Fatal("failure not reported")
	}
}

func TestBoardFromFiles(t *testing.T) {
	dir := t.TempDir()
	releaseFile, sysinfoDir = filepath.Join(dir, "openwrt_release"), dir
	defer func() { releaseFile, sysinfoDir = "/etc/openwrt_release", "/tmp/sysinfo" }()
	os.WriteFile(releaseFile, []byte("DISTRIB_ID='OpenWrt'\nDISTRIB_RELEASE='23.05.4'\nDISTRIB_REVISION='r24012-d8dd03c46f'\nDISTRIB_TARGET='ipq40xx/chromium'\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "board_name"), []byte("google,wifi\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "model"), []byte("Google WiFi (Gale)\n"), 0o644)
	b, err := boardFromFiles(errors.New("ubus down"))
	if err != nil || b.Version != "23.05.4" || b.Target != "ipq40xx/chromium" || b.BoardName != "google,wifi" || b.Model != "Google WiFi (Gale)" {
		t.Fatalf("%+v %v", b, err)
	}
	os.Remove(filepath.Join(dir, "board_name"))
	if _, err := boardFromFiles(errors.New("ubus down")); err == nil {
		t.Fatal("no board name must be an error, never a guess")
	}
}
