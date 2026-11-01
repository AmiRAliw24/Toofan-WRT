// Package firmware upgrades OpenWrt itself (sysupgrade) from the panel,
// keeping every setting and the panel.
//
// Safety, in order:
//   - only official releases for this exact board (profiles.json lists the
//     board names each image accepts), only newer ones, and at most one
//     release series ahead (23.05 → 24.10 → 25.12), never release candidates;
//   - the image is built by OpenWrt's own image builder (ASU) with the
//     panel's packages, falling back to the official image;
//   - the download must match the published SHA-256 and pass sysupgrade -T
//     (which also refuses images whose settings are incompatible);
//   - free RAM is checked for the image and the settings backup;
//   - settings are kept; the panel, its Xray engines and geo files go into
//     the settings backup, and a one-shot first-boot script puts the panel
//     back in charge. After the reboot the panel installs whatever packages
//     the new firmware lacks (Heal).
package firmware

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"toofanwrt/internal/env"
	"toofanwrt/internal/fetch"
)

var (
	downloadsURL = "https://downloads.openwrt.org"
	asuURL       = "https://sysupgrade.openwrt.org"
	WorkDir      = "/tmp/toofanwrt-fw"
	MarkerPath   = "/etc/toofanwrt/firmware.json"
	KeepPath     = "/lib/upgrade/keep.d/toofanwrt-firmware"
	RestorePath  = "/etc/uci-defaults/99-toofanwrt-restore"

	boardFn     = readBoard
	releaseFile = "/etc/openwrt_release"
	sysinfoDir  = "/tmp/sysinfo"
	memAvailFn  = memAvailable
	testFn      = func(img string) (string, error) { return env.Run(2*time.Minute, "sysupgrade", "-T", img) }
	flashFn     = startSysupgrade
	pollEvery   = 5 * time.Second
	buildLimit  = 15 * time.Minute
)

// Packages the panel needs; the image builder adds them to the new firmware.
// LuCI stays in the image as a safety net and is removed after the panel
// comes back (when it was removed before).
var (
	Required = []string{"xray-core", "v2ray-geoip", "v2ray-geosite-ir", "kmod-nft-tproxy", "ca-bundle"}
	Optional = []string{"openvpn-mbedtls", "miniupnpd-nftables", "tc-tiny", "kmod-sched-cake", "kmod-ifb", "kmod-nft-bridge"}
)

// keepFiles go into the settings backup on top of the usual ones.
var keepFiles = []string{
	"/usr/bin/toofanwrt",
	"/etc/init.d/toofanwrt",
	"/etc/rc.d/S99toofanwrt",
	"/etc/rc.d/K10toofanwrt",
	"/etc/hotplug.d/iface/99-toofanwrt",
	"/lib/upgrade/keep.d/toofanwrt",
	"/usr/libexec/toofanwrt/",
	"/usr/share/toofanwrt/",
	"/usr/share/toofanwrt-uninstall.sh",
	"/etc/toofanwrt/",
}

// restoreScript runs once on the first boot of the new firmware (uci-defaults
// run before the web server starts) and gives port 80 back to the panel.
const restoreScript = `#!/bin/sh
# Written by ToofanWrt before a firmware upgrade; runs once on the first boot.
[ -x /usr/bin/toofanwrt ] || exit 0
/etc/init.d/toofanwrt enable
if [ -f /etc/config/uhttpd ]; then
	uci -q delete uhttpd.main.listen_http
	uci add_list uhttpd.main.listen_http='0.0.0.0:8081'
	uci -q delete uhttpd.main.listen_https
	uci set uhttpd.main.redirect_https='0'
	uci commit uhttpd
	[ -x /etc/init.d/uhttpd ] && /etc/init.d/uhttpd disable
fi
[ -x /etc/init.d/xray ] && /etc/init.d/xray disable
if [ "$(uci -q get firewall.@defaults[0].flow_offloading)" = "1" ]; then
	uci set firewall.@defaults[0].flow_offloading=0
	uci commit firewall
fi
exit 0
`

type Board struct {
	Model     string `json:"model"`
	BoardName string `json:"boardName"`
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	Target    string `json:"target"`
	Compat    string `json:"compat"`
	PM        string `json:"pm"` // opkg | apk
}

func readBoard() (Board, error) {
	if env.Mock {
		return Board{Model: "Google WiFi", BoardName: "google,wifi", Version: "23.05.4", Revision: "r24106-10cc5fcd00", Target: "ipq40xx/chromium", Compat: "1.1", PM: "opkg"}, nil
	}
	out, err := env.Ubus("system", "board", "")
	if err != nil {
		return boardFromFiles(err)
	}
	var raw struct {
		Model     string `json:"model"`
		BoardName string `json:"board_name"`
		Release   struct {
			Version  string `json:"version"`
			Revision string `json:"revision"`
			Target   string `json:"target"`
		} `json:"release"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return Board{}, err
	}
	b := Board{Model: raw.Model, BoardName: raw.BoardName, Version: raw.Release.Version,
		Revision: raw.Release.Revision, Target: raw.Release.Target, PM: "opkg"}
	b.Compat, _ = env.Run(5*time.Second, "uci", "-q", "get", "system.@system[0].compat_version")
	if b.Compat == "" {
		b.Compat = "1.0"
	}
	if _, err := os.Stat("/usr/bin/apk"); err == nil {
		b.PM = "apk"
	}
	return b, nil
}

// boardFromFiles reads the same facts without ubus (procd busy or absent).
func boardFromFiles(ubusErr error) (Board, error) {
	rel, err := os.ReadFile(releaseFile)
	if err != nil {
		return Board{}, ubusErr
	}
	b := Board{PM: "opkg", Compat: "1.0"}
	for _, l := range strings.Split(string(rel), "\n") {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `'"`)
		switch k {
		case "DISTRIB_RELEASE":
			b.Version = v
		case "DISTRIB_REVISION":
			b.Revision = v
		case "DISTRIB_TARGET":
			b.Target = v
		}
	}
	read := func(p string) string { x, _ := os.ReadFile(p); return strings.TrimSpace(string(x)) }
	b.BoardName, b.Model = read(sysinfoDir+"/board_name"), read(sysinfoDir+"/model")
	if c, _ := env.Run(5*time.Second, "uci", "-q", "get", "system.@system[0].compat_version"); c != "" {
		b.Compat = c
	}
	if _, err := os.Stat("/usr/bin/apk"); err == nil {
		b.PM = "apk"
	}
	if b.Version == "" || b.Target == "" || b.BoardName == "" {
		return Board{}, ubusErr
	}
	return b, nil
}

// ---- versions ----

// Option is one release offered on the page.
type Option struct {
	Version string `json:"version"`
	Series  string `json:"series"`
	Current bool   `json:"current"`
	Allowed bool   `json:"allowed"`
	// Reason when not allowed: current | older | step (upgrade to an
	// earlier series first).
	Reason  string `json:"reason,omitempty"`
	Stable  bool   `json:"stable"` // OpenWrt's current stable release
	PM      string `json:"pm"`     // package manager of that release
	Notes   string `json:"notes"`
	Changes bool   `json:"changes"` // a new series (bigger change than a fix release)
}

type ver struct {
	major, minor, patch int
	s                   string
}

func parseVer(s string) (ver, bool) {
	if strings.Contains(s, "-") { // rc, snapshot
		return ver{}, false
	}
	p := strings.Split(s, ".")
	if len(p) != 3 {
		return ver{}, false
	}
	var n [3]int
	for i, x := range p {
		v, err := strconv.Atoi(x)
		if err != nil {
			return ver{}, false
		}
		n[i] = v
	}
	return ver{n[0], n[1], n[2], s}, true
}

func (v ver) series() int { return v.major*100 + v.minor }
func (v ver) less(o ver) bool {
	if v.major != o.major {
		return v.major < o.major
	}
	if v.minor != o.minor {
		return v.minor < o.minor
	}
	return v.patch < o.patch
}

// Options picks, per release series, its newest release, and decides which
// of them can be installed from current.
func Options(current string, list []string, stable string) []Option {
	cur, okCur := parseVer(current)
	latest := map[int]ver{}
	for _, s := range list {
		v, ok := parseVer(s)
		if !ok {
			continue
		}
		if l, ok := latest[v.series()]; !ok || l.less(v) {
			latest[v.series()] = v
		}
	}
	var series []int
	for s := range latest {
		if !okCur || s >= cur.series() {
			series = append(series, s)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(series)))
	// The series right after the current one.
	next := 0
	for _, s := range series {
		if okCur && s > cur.series() && (next == 0 || s < next) {
			next = s
		}
	}
	var out []Option
	for _, s := range series {
		v := latest[s]
		o := Option{Version: v.s, Series: fmt.Sprintf("%d.%02d", v.major, v.minor), Stable: v.s == stable, PM: "opkg",
			Notes: fmt.Sprintf("https://openwrt.org/releases/%d.%02d/notes-%s", v.major, v.minor, v.s)}
		if v.major >= 25 {
			o.PM = "apk"
		}
		switch {
		case !okCur:
			o.Reason = "older"
		case v.s == cur.s:
			o.Current, o.Reason = true, "current"
		case !cur.less(v):
			o.Reason = "older"
		case s == cur.series() || s == next:
			o.Allowed, o.Changes = true, s != cur.series()
		default:
			o.Reason, o.Changes = "step", true
		}
		out = append(out, o)
	}
	return out
}

var (
	verMu    sync.Mutex
	verCache struct {
		list   []string
		stable string
	}
	verAt time.Time
)

func releases() ([]string, string, error) {
	verMu.Lock()
	defer verMu.Unlock()
	if time.Since(verAt) < 30*time.Minute && len(verCache.list) > 0 {
		return verCache.list, verCache.stable, nil
	}
	var v struct {
		Stable string   `json:"stable_version"`
		List   []string `json:"versions_list"`
	}
	if env.Mock {
		v.Stable, v.List = "25.12.6", []string{"25.12.6", "25.12.0", "24.10.8", "24.10.0", "23.05.6", "23.05.4", "22.03.7"}
	} else if code, err := fetch.JSON("GET", downloadsURL+"/.versions.json", nil, &v, time.Minute); err != nil || code != 200 {
		if err == nil {
			err = fmt.Errorf("HTTP %d", code)
		}
		return nil, "", fmt.Errorf("could not read the release list from downloads.openwrt.org: %v", err)
	}
	verCache.list, verCache.stable, verAt = v.List, v.Stable, time.Now()
	return v.List, v.Stable, nil
}

// ---- overview ----

type Overview struct {
	Board   Board    `json:"board"`
	Options []Option `json:"options"`
	Error   string   `json:"error,omitempty"`
	Job     Job      `json:"job"`
	Last    *Marker  `json:"last,omitempty"` // the previous upgrade
	Heal    Heal     `json:"heal"`
	// MemFree is the RAM available now, in bytes.
	MemFree int64 `json:"memFree"`
}

func Get() Overview {
	o := Overview{Job: CurrentJob(), Heal: HealStatus(), MemFree: memAvailFn()}
	b, err := boardFn()
	if err != nil {
		o.Error = "could not read the router model: " + err.Error()
		return o
	}
	o.Board = b
	if m, err := ReadMarker(); err == nil {
		o.Last = &m
	}
	list, stable, err := releases()
	if err != nil {
		o.Error = err.Error()
		return o
	}
	o.Options = Options(b.Version, list, stable)
	return o
}

// ---- image lookup ----

type image struct {
	URL    string
	Name   string
	SHA256 string
}

type profiles struct {
	Profiles map[string]struct {
		SupportedDevices []string `json:"supported_devices"`
		Images           []struct {
			Type   string `json:"type"`
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		} `json:"images"`
	} `json:"profiles"`
}

// officialImage finds this board's sysupgrade image in a release.
func officialImage(version string, b Board) (string, image, error) {
	base := fmt.Sprintf("%s/releases/%s/targets/%s/", downloadsURL, version, b.Target)
	var p profiles
	code, err := fetch.JSON("GET", base+"profiles.json", nil, &p, 2*time.Minute)
	if err == nil && code != 200 {
		err = fmt.Errorf("HTTP %d", code)
	}
	if err != nil {
		return "", image{}, fmt.Errorf("could not read the image list of %s: %v", version, err)
	}
	return findImage(p, base, b.BoardName, version)
}

func findImage(p profiles, base, boardName, version string) (string, image, error) {
	ids := make([]string, 0, len(p.Profiles))
	for id := range p.Profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		pr := p.Profiles[id]
		match := false
		for _, d := range pr.SupportedDevices {
			if d == boardName {
				match = true
			}
		}
		if !match {
			continue
		}
		for _, im := range pr.Images {
			if im.Type == "sysupgrade" && len(im.SHA256) == 64 {
				return id, image{URL: base + im.Name, Name: im.Name, SHA256: im.SHA256}, nil
			}
		}
		return "", image{}, fmt.Errorf("OpenWrt %s has no upgrade image for this router", version)
	}
	return "", image{}, fmt.Errorf("OpenWrt %s does not support this router (%s)", version, boardName)
}

// ---- custom build (ASU) ----

type asuRequest struct {
	Profile      string   `json:"profile"`
	Target       string   `json:"target"`
	Version      string   `json:"version"`
	Packages     []string `json:"packages"`
	DiffPackages bool     `json:"diff_packages"`
	Client       string   `json:"client"`
}

type asuAnswer struct {
	Status      int    `json:"status"`
	Detail      string `json:"detail"`
	Stage       string `json:"imagebuilder_status"`
	RequestHash string `json:"request_hash"`
	BinDir      string `json:"bin_dir"`
	Images      []struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
	} `json:"images"`
	Stderr string `json:"stderr"`
}

// customImage asks OpenWrt's image builder for the release with the
// panel's packages already inside.
func customImage(version, profile string, b Board, pkgs []string, stage func(string)) (image, error) {
	req := asuRequest{Profile: profile, Target: b.Target, Version: version, Packages: pkgs, Client: "toofanwrt"}
	var a asuAnswer
	code, err := fetch.JSON("POST", asuURL+"/api/v1/build", req, &a, 2*time.Minute)
	deadline := time.Now().Add(buildLimit)
	for {
		if err != nil {
			return image{}, err
		}
		switch {
		case code == 200 && len(a.Images) > 0:
			for _, im := range a.Images {
				if im.Type == "sysupgrade" && len(im.SHA256) == 64 && a.BinDir != "" {
					return image{URL: fmt.Sprintf("%s/store/%s/%s", asuURL, a.BinDir, im.Name), Name: im.Name, SHA256: im.SHA256}, nil
				}
			}
			return image{}, errors.New("the image builder returned no upgrade image")
		case code == 202 && a.RequestHash != "":
			stage(a.Stage)
		default:
			msg := a.Detail
			if msg == "" {
				msg = fmt.Sprintf("HTTP %d", code)
			}
			if a.Stderr != "" {
				msg += ": " + lastLine(a.Stderr)
			}
			return image{}, errors.New(msg)
		}
		if time.Now().After(deadline) {
			return image{}, errors.New("the image builder took too long")
		}
		if cancelled() {
			return image{}, errCancelled
		}
		time.Sleep(pollEvery)
		hash := a.RequestHash
		a = asuAnswer{}
		code, err = fetch.JSON("GET", asuURL+"/api/v1/build/"+hash, nil, &a, time.Minute)
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	l := strings.TrimSpace(lines[len(lines)-1])
	if len(l) > 200 {
		l = l[:200]
	}
	return l
}

// ---- job ----

type Job struct {
	State   string `json:"state"` // idle | building | downloading | checking | ready | flashing | failed
	Version string `json:"version,omitempty"`
	Custom  bool   `json:"custom"`
	// Fallback says why the custom image could not be used.
	Fallback string `json:"fallback,omitempty"`
	Stage    string `json:"stage,omitempty"`
	Done     int64  `json:"done"`
	Total    int64  `json:"total"`
	Image    string `json:"image,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Size     int64  `json:"size"`
	KitSize  int64  `json:"kitSize"` // panel files carried in the settings backup
	Error    string `json:"error,omitempty"`
	Started  int64  `json:"started,omitempty"`
}

var (
	jobMu  sync.Mutex
	job    = Job{State: "idle"}
	cancel bool

	errCancelled = errors.New("cancelled")
)

func CurrentJob() Job {
	jobMu.Lock()
	defer jobMu.Unlock()
	return job
}

func update(f func(j *Job)) {
	jobMu.Lock()
	f(&job)
	jobMu.Unlock()
}

func cancelled() bool {
	jobMu.Lock()
	defer jobMu.Unlock()
	return cancel
}

func busy(state string) bool {
	switch state {
	case "building", "downloading", "checking", "flashing":
		return true
	}
	return false
}

func imagePath() string { return filepath.Join(WorkDir, "firmware.bin") }

// Start prepares (builds, downloads and checks) an upgrade image. Nothing
// is written to flash until Flash is called.
func Start(version string, custom bool) error {
	b, err := boardFn()
	if err != nil {
		return err
	}
	list, stable, err := releases()
	if err != nil {
		return err
	}
	allowed := false
	for _, o := range Options(b.Version, list, stable) {
		if o.Version == version && o.Allowed {
			allowed = true
		}
	}
	if !allowed {
		return fmt.Errorf("OpenWrt %s can't be installed from %s", version, b.Version)
	}
	jobMu.Lock()
	if busy(job.State) {
		jobMu.Unlock()
		return errors.New("an upgrade is already being prepared")
	}
	job = Job{State: "building", Version: version, Custom: custom, Started: time.Now().Unix()}
	if !custom {
		job.State = "downloading"
	}
	cancel = false
	jobMu.Unlock()
	go prepare(version, custom, b)
	return nil
}

func fail(err error) {
	os.RemoveAll(WorkDir)
	update(func(j *Job) {
		if errors.Is(err, errCancelled) {
			*j = Job{State: "idle"}
			return
		}
		j.State, j.Error = "failed", err.Error()
	})
}

func prepare(version string, custom bool, b Board) {
	if env.Mock {
		mockPrepare(version, custom)
		return
	}
	// Room for the image (≤ 64 MB) before anything is downloaded.
	if free := memAvailFn(); free > 0 && free < 96<<20 {
		fail(fmt.Errorf("not enough free memory (%d MB; 96 MB needed)", free>>20))
		return
	}
	profile, img, err := officialImage(version, b)
	if err != nil {
		fail(err)
		return
	}
	if custom {
		stage := func(s string) { update(func(j *Job) { j.Stage = s }) }
		cimg, err := customImage(version, profile, b, append(append([]string{"luci"}, Required...), Optional...), stage)
		if err != nil && !errors.Is(err, errCancelled) {
			// Optional packages missing from that release's feed are the usual
			// cause: try once more with just the required ones.
			env.Logf("firmware: custom build: %v; retrying with the required packages only", err)
			cimg, err = customImage(version, profile, b, append([]string{"luci"}, Required...), stage)
		}
		switch {
		case errors.Is(err, errCancelled):
			fail(err)
			return
		case err != nil:
			env.Logf("firmware: custom build failed, using the official image: %v", err)
			update(func(j *Job) { j.Custom, j.Fallback = false, err.Error() })
		default:
			img = cimg
		}
	}
	update(func(j *Job) { j.State, j.Stage, j.Image = "downloading", "", img.Name })
	os.RemoveAll(WorkDir)
	if err := os.MkdirAll(WorkDir, 0o755); err != nil {
		fail(err)
		return
	}
	sum, _, err := fetch.ToFile(img.URL, imagePath(), 64<<20, func(done, total int64) {
		update(func(j *Job) { j.Done, j.Total = done, total })
	})
	if err != nil {
		fail(fmt.Errorf("download failed: %v", err))
		return
	}
	if cancelled() {
		fail(errCancelled)
		return
	}
	if !strings.EqualFold(sum, img.SHA256) {
		fail(fmt.Errorf("the downloaded image is damaged (SHA-256 %s, expected %s)", sum, img.SHA256))
		return
	}
	st, _ := os.Stat(imagePath())
	kit := kitSize()
	update(func(j *Job) { j.State, j.SHA256, j.Size, j.KitSize = "checking", sum, st.Size(), kit })
	// The settings backup is built in RAM while the image is still there,
	// and the new firmware unpacks it in RAM on its first boot.
	if free := memAvailFn(); free > 0 && free < kit+48<<20 {
		fail(fmt.Errorf("not enough free memory for the settings backup (%d MB free, %d MB needed)", free>>20, (kit+48<<20)>>20))
		return
	}
	if out, err := testFn(imagePath()); err != nil {
		fail(fmt.Errorf("sysupgrade refused the image: %s", firstNonEmpty(strings.TrimSpace(out), err.Error())))
		return
	}
	update(func(j *Job) { j.State = "ready" })
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// kitSize adds up the panel files that ride along in the settings backup.
func kitSize() int64 {
	var n int64
	for _, p := range keepFiles {
		filepath.Walk(p, func(_ string, fi os.FileInfo, err error) error {
			if err == nil && fi.Mode().IsRegular() {
				n += fi.Size()
			}
			return nil
		})
	}
	return n
}

// Cancel drops a prepared (or in-progress) image.
func Cancel() error {
	jobMu.Lock()
	defer jobMu.Unlock()
	if job.State == "flashing" {
		return errors.New("the upgrade has already started")
	}
	cancel = true
	if !busy(job.State) {
		job = Job{State: "idle"}
		os.RemoveAll(WorkDir)
	}
	return nil
}

// ---- flash ----

// Marker records an upgrade so the new firmware can finish it.
type Marker struct {
	From       string `json:"from"`
	To         string `json:"to"`
	At         int64  `json:"at"`
	Custom     bool   `json:"custom"`
	RemoveLuci bool   `json:"removeLuci"`
	// Set by the new firmware:
	Healed   bool   `json:"healed"`
	HealedAt int64  `json:"healedAt,omitempty"`
	Result   string `json:"result,omitempty"`
}

func ReadMarker() (Marker, error) {
	var m Marker
	b, err := os.ReadFile(MarkerPath)
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

func writeMarker(m Marker) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.MkdirAll(filepath.Dir(MarkerPath), 0o755); err != nil {
		return err
	}
	tmp := MarkerPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, MarkerPath)
}

// LuciPresent tells whether LuCI is installed now (set by the caller).
var LuciPresent = func() bool { _, err := os.Stat("/etc/init.d/uhttpd"); return err == nil }

// Flash writes the prepared image. The router reboots by itself.
func Flash() error {
	jobMu.Lock()
	if job.State != "ready" {
		jobMu.Unlock()
		return errors.New("no checked image is ready")
	}
	j := job
	job.State = "flashing"
	jobMu.Unlock()
	if env.Mock {
		go func() {
			time.Sleep(8 * time.Second)
			update(func(j *Job) { *j = Job{State: "idle"} })
		}()
		return nil
	}
	b, _ := boardFn()
	undo := func(err error) error {
		os.Remove(KeepPath)
		os.Remove(RestorePath)
		update(func(j *Job) { j.State, j.Error = "failed", err.Error() })
		return err
	}
	if err := writeMarker(Marker{From: b.Version, To: j.Version, At: time.Now().Unix(), Custom: j.Custom, RemoveLuci: !LuciPresent()}); err != nil {
		return undo(err)
	}
	if err := os.MkdirAll(filepath.Dir(RestorePath), 0o755); err != nil {
		return undo(err)
	}
	if err := os.WriteFile(RestorePath, []byte(restoreScript), 0o755); err != nil {
		return undo(err)
	}
	keep := strings.Join(append(append([]string{}, keepFiles...), RestorePath, KeepPath), "\n") + "\n"
	if err := os.WriteFile(KeepPath, []byte(keep), 0o644); err != nil {
		return undo(err)
	}
	syscall.Sync()
	if err := flashFn(imagePath()); err != nil {
		os.Remove(MarkerPath)
		return undo(fmt.Errorf("sysupgrade did not start: %v", err))
	}
	return nil
}

// startSysupgrade runs sysupgrade in its own session: it stops every
// service (this panel too) and reboots when the image is written.
func startSysupgrade(img string) error {
	logf, err := os.Create(filepath.Join(WorkDir, "sysupgrade.log"))
	if err != nil {
		return err
	}
	cmd := exec.Command("sysupgrade", img)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logf.Close()
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); logf.Close() }()
	// A refusal comes within seconds; success never returns (the router
	// reboots), or returns 0 once procd has taken over.
	select {
	case err := <-done:
		if err != nil {
			out, _ := os.ReadFile(filepath.Join(WorkDir, "sysupgrade.log"))
			return fmt.Errorf("%v: %s", err, lastLine(string(out)))
		}
	case <-time.After(20 * time.Second):
	}
	return nil
}

// ---- mock ----

func mockPrepare(version string, custom bool) {
	step := func(d time.Duration) bool {
		time.Sleep(d)
		return cancelled()
	}
	name := "openwrt-" + version + "-ipq40xx-chromium-google_wifi-squashfs-sysupgrade.bin"
	if custom {
		for _, s := range []string{"validate_manifest", "building_image"} {
			update(func(j *Job) { j.Stage = s })
			if step(2 * time.Second) {
				fail(errCancelled)
				return
			}
		}
	}
	update(func(j *Job) { j.State, j.Stage, j.Image, j.Total = "downloading", "", name, 18914353 })
	for i := int64(1); i <= 10; i++ {
		if step(300 * time.Millisecond) {
			fail(errCancelled)
			return
		}
		update(func(j *Job) { j.Done = j.Total * i / 10 })
	}
	update(func(j *Job) {
		j.State, j.SHA256, j.Size, j.KitSize = "checking", "164845396716a1c50883204947d8a0c822b8fd303d28d980aa1ca46d1f2c4f8e", j.Total, 98<<20
	})
	time.Sleep(time.Second)
	update(func(j *Job) { j.State = "ready" })
}

func memAvailable() int64 {
	if env.Mock {
		return 380 << 20
	}
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == "MemAvailable:" {
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			return kb << 10
		}
	}
	return 0
}
