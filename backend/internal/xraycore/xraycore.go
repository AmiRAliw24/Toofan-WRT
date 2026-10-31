// Package xraycore installs official Xray-core releases on the router (the
// "Xray version" switch of 3x-ui): it lists releases, downloads the one for
// this CPU, checks it against the published SHA-256, swaps it in and rolls
// back when the engine does not come up with it.
package xraycore

import (
	"archive/zip"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"toofanwrt/internal/env"
	"toofanwrt/internal/fetch"
	"toofanwrt/internal/uci"
)

var (
	Dir         = "/usr/libexec/toofanwrt"
	releasesURL = "https://api.github.com/repos/XTLS/Xray-core/releases?per_page=40"
	// The Atom feed is the fallback when the API is rate-limited (60/h).
	atomURL     = "https://github.com/XTLS/Xray-core/releases.atom"
	downloadURL = "https://github.com/XTLS/Xray-core/releases/download/"
)

// MinVersion is the oldest release the panel's configs are tested with.
var MinVersion = [3]int{24, 12, 31}

func MainPath() string   { return filepath.Join(Dir, "xray") }
func PrevPath() string   { return filepath.Join(Dir, "xray.prev") }
func CompatPath() string { return filepath.Join(Dir, "xray-compat") }

// Asset is the release file for this router's CPU.
func Asset() string {
	switch runtime.GOARCH {
	case "arm":
		return "Xray-linux-arm32-v7a.zip"
	case "arm64":
		return "Xray-linux-arm64-v8a.zip"
	case "mipsle":
		return "Xray-linux-mips32le.zip"
	case "mips":
		return "Xray-linux-mips32.zip"
	}
	return "Xray-linux-64.zip"
}

type Release struct {
	Tag        string `json:"tag"`
	Version    string `json:"version"`
	Published  int64  `json:"published"`
	Prerelease bool   `json:"prerelease"`
	URL        string `json:"url"` // release page
}

var (
	relMu    sync.Mutex
	relCache []Release
	relAt    time.Time
)

var verRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// ParseVersion reads "v26.10.29" / "Xray 26.10.29 (…)" into numbers.
func ParseVersion(s string) ([3]int, bool) {
	m := verRe.FindStringSubmatch(s)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := 0; i < 3; i++ {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

func less(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// Releases lists installable releases, newest first (cached 10 minutes).
func Releases() ([]Release, error) {
	relMu.Lock()
	defer relMu.Unlock()
	if relCache != nil && time.Since(relAt) < 10*time.Minute {
		return relCache, nil
	}
	if env.Mock {
		relCache = []Release{
			{Tag: "v26.10.31", Version: "26.10.31", Published: time.Now().Add(-8 * time.Hour).Unix()},
			{Tag: "v26.10.21", Version: "26.10.21", Published: time.Now().Add(-10 * 24 * time.Hour).Unix()},
			{Tag: "v26.9.30", Version: "26.9.30", Published: time.Now().Add(-31 * 24 * time.Hour).Unix()},
			{Tag: "v26.3.27", Version: "26.3.27", Published: time.Now().Add(-218 * 24 * time.Hour).Unix()},
			{Tag: "v25.1.30", Version: "25.1.30", Published: time.Now().Add(-640 * 24 * time.Hour).Unix()},
		}
		relAt = time.Now()
		return relCache, nil
	}
	out, err := apiReleases()
	if err != nil {
		var aerr error
		if out, aerr = atomReleases(); aerr != nil {
			return nil, fmt.Errorf("GitHub: %v", err)
		}
	}
	relCache, relAt = out, time.Now()
	return out, nil
}

func apiReleases() ([]Release, error) {
	resp, err := fetch.Get(releasesURL, time.Minute)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw []struct {
		Tag        string `json:"tag_name"`
		Published  string `json:"published_at"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
		HTMLURL    string `json:"html_url"`
		Assets     []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	out := []Release{}
	for _, r := range raw {
		v, ok := ParseVersion(r.Tag)
		if r.Draft || !ok || less(v, MinVersion) {
			continue
		}
		has := false
		for _, a := range r.Assets {
			has = has || a.Name == Asset()
		}
		if !has {
			continue
		}
		t, _ := time.Parse(time.RFC3339, r.Published)
		out = append(out, Release{Tag: r.Tag, Version: fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]),
			Published: t.Unix(), Prerelease: r.Prerelease, URL: r.HTMLURL})
	}
	if len(out) == 0 {
		return nil, errors.New("no releases listed")
	}
	return out, nil
}

// atomReleases reads the public release feed (no asset or pre-release
// details there; every Xray release ships the Linux builds).
func atomReleases() ([]Release, error) {
	resp, err := fetch.Get(atomURL, time.Minute)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parseAtom(io.LimitReader(resp.Body, 4<<20))
}

func parseAtom(r io.Reader) ([]Release, error) {
	var feed struct {
		Entries []struct {
			Updated string `xml:"updated"`
			Links   []struct {
				Href string `xml:"href,attr"`
			} `xml:"link"`
		} `xml:"entry"`
	}
	if err := xml.NewDecoder(r).Decode(&feed); err != nil {
		return nil, err
	}
	out := []Release{}
	for _, e := range feed.Entries {
		for _, l := range e.Links {
			i := strings.LastIndex(l.Href, "/releases/tag/")
			if i < 0 {
				continue
			}
			tag := l.Href[i+len("/releases/tag/"):]
			v, ok := ParseVersion(tag)
			if !ok || less(v, MinVersion) {
				continue
			}
			t, _ := time.Parse(time.RFC3339, e.Updated)
			out = append(out, Release{Tag: tag, Version: fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]), Published: t.Unix(), URL: l.Href})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no releases in the feed")
	}
	return out, nil
}

// Version runs `<bin> version` and returns "26.10.29" ("" if it won't run).
func Version(bin string) string {
	if env.Mock {
		switch filepath.Base(bin) {
		case "xray-compat":
			return "26.3.27"
		case "xray.prev":
			return mockPrev
		}
		return mockMain
	}
	if bin == "" {
		return ""
	}
	out, err := env.Run(10*time.Second, bin, "version")
	if err != nil {
		return ""
	}
	if v, ok := ParseVersion(out); ok {
		return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
	}
	return ""
}

var mockMain, mockPrev = "26.10.31", ""

// Job is the state of an install.
type Job struct {
	Running bool   `json:"running"`
	Tag     string `json:"tag,omitempty"`
	Stage   string `json:"stage,omitempty"` // download | verify | test | switch | done | failed | rolled back
	Done    int64  `json:"done,omitempty"`
	Total   int64  `json:"total,omitempty"`
	Error   string `json:"error,omitempty"`
	At      int64  `json:"at,omitempty"`
}

var (
	jobMu sync.Mutex
	job   Job
)

func CurrentJob() Job {
	jobMu.Lock()
	defer jobMu.Unlock()
	return job
}

func setJob(f func(*Job)) {
	jobMu.Lock()
	f(&job)
	jobMu.Unlock()
}

// SetEngine switches the running engine to a binary and restarts it; it
// fails when the current server does not come back up.
type SetEngine func(bin string) error

// Start installs tag in the background. current is the engine in use, to
// return to if the new one fails and there is no previous copy.
func Start(tag, current string, setEngine SetEngine) error {
	jobMu.Lock()
	if job.Running {
		jobMu.Unlock()
		return errors.New("an install is already running")
	}
	job = Job{Running: true, Tag: tag, Stage: "download"}
	jobMu.Unlock()
	go func() {
		err := install(tag, current, setEngine)
		setJob(func(j *Job) {
			j.Running, j.At = false, time.Now().Unix()
			switch {
			case err == nil:
				j.Stage, j.Error = "done", ""
			case errors.Is(err, errRolledBack):
				j.Stage, j.Error = "rolled back", err.Error()
			default:
				j.Stage, j.Error = "failed", err.Error()
			}
		})
	}()
	return nil
}

var errRolledBack = errors.New("rolled back")

func freeMB(dir string) int64 {
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize) >> 20
}

func install(tag, current string, setEngine SetEngine) error {
	want, ok := ParseVersion(tag)
	if !ok || less(want, MinVersion) {
		return fmt.Errorf("version %s is not supported (oldest: %d.%d.%d)", tag, MinVersion[0], MinVersion[1], MinVersion[2])
	}
	if env.Mock {
		for _, st := range []string{"download", "verify", "test", "switch"} {
			setJob(func(j *Job) { j.Stage, j.Done, j.Total = st, 9<<20, 16<<20 })
			time.Sleep(600 * time.Millisecond)
		}
		mockPrev, mockMain = mockMain, fmt.Sprintf("%d.%d.%d", want[0], want[1], want[2])
		return nil
	}
	if err := os.MkdirAll(Dir, 0o755); err != nil {
		return err
	}
	if mb := freeMB(Dir); mb >= 0 && mb < 90 {
		return fmt.Errorf("not enough free space on the router (%d MB, 90 MB needed)", mb)
	}
	url := downloadURL + tag + "/" + Asset()
	zipPath := filepath.Join(Dir, "download.zip")
	defer os.Remove(zipPath)
	got, _, err := fetch.ToFile(url, zipPath, 64<<20, func(done, total int64) {
		setJob(func(j *Job) { j.Done, j.Total = done, total })
	})
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	setJob(func(j *Job) { j.Stage = "verify" })
	dgst, err := fetch.Text(url + ".dgst")
	if err != nil {
		return fmt.Errorf("checksum file: %w", err)
	}
	if sum := fetch.SHA256Line(dgst); sum == "" || sum != got {
		return errors.New("checksum mismatch: the download is not the official release")
	}
	newPath := filepath.Join(Dir, "xray.new")
	if err := extract(zipPath, "xray", newPath); err != nil {
		return err
	}
	setJob(func(j *Job) { j.Stage = "test" })
	if v := Version(newPath); v != fmt.Sprintf("%d.%d.%d", want[0], want[1], want[2]) {
		os.Remove(newPath)
		return errors.New("the new Xray does not run on this router")
	}
	setJob(func(j *Job) { j.Stage = "switch" })
	if _, err := os.Stat(MainPath()); err == nil {
		os.Remove(PrevPath())
		if err := os.Rename(MainPath(), PrevPath()); err != nil {
			return err
		}
	}
	if err := os.Rename(newPath, MainPath()); err != nil {
		return err
	}
	if err := setEngine(MainPath()); err != nil {
		if rerr := Rollback(setEngine); rerr == nil {
			return fmt.Errorf("%w: the engine did not start with %s (%v); the previous version is back", errRolledBack, tag, err)
		}
		if current != "" && current != MainPath() {
			setEngine(current)
			return fmt.Errorf("%w: the engine did not start with %s (%v); %s is back", errRolledBack, tag, err, current)
		}
		return fmt.Errorf("the engine did not start with %s: %v", tag, err)
	}
	pin(MainPath())
	return nil
}

// pin makes the chosen binary the configured engine and keeps panel
// updates from replacing it with the bundled one.
func pin(bin string) {
	uci.NewBatch().Set("toofanwrt.main.xray", bin).Set("toofanwrt.main.xray_pinned", "1").Commit()
}

// Rollback swaps back the version from before the last install.
func Rollback(setEngine SetEngine) error {
	if env.Mock {
		if mockPrev == "" {
			return errors.New("no previous version")
		}
		mockMain, mockPrev = mockPrev, mockMain
		return nil
	}
	if _, err := os.Stat(PrevPath()); err != nil {
		return errors.New("no previous version")
	}
	tmp := filepath.Join(Dir, "xray.swap")
	os.Rename(MainPath(), tmp)
	if err := os.Rename(PrevPath(), MainPath()); err != nil {
		os.Rename(tmp, MainPath())
		return err
	}
	os.Rename(tmp, PrevPath())
	pin(MainPath())
	return setEngine(MainPath())
}

func extract(zipPath, name, dst string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("bad archive: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		if f.UncompressedSize64 > 128<<20 {
			return errors.New("binary too large")
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, io.LimitReader(rc, 128<<20)); err != nil {
			out.Close()
			os.Remove(dst)
			return err
		}
		return out.Close()
	}
	return fmt.Errorf("%s not found in the release archive", name)
}

// Installed describes the engines on the router.
type Installed struct {
	Main   Engine `json:"main"`
	Compat Engine `json:"compat"`
	Prev   string `json:"prev,omitempty"`
	Asset  string `json:"asset"`
	Job    Job    `json:"job"`
	Min    string `json:"min"`
	Pinned bool   `json:"pinned"`
}

type Engine struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

func Describe(mainBin, compatBin string) Installed {
	in := Installed{
		Main:  Engine{Path: mainBin, Version: Version(mainBin)},
		Asset: Asset(), Job: CurrentJob(),
		Min: fmt.Sprintf("%d.%d.%d", MinVersion[0], MinVersion[1], MinVersion[2]),
	}
	if compatBin != "" {
		in.Compat = Engine{Path: compatBin, Version: Version(compatBin)}
	}
	if env.Mock {
		in.Prev = mockPrev
		in.Compat = Engine{Path: CompatPath(), Version: "26.3.27"}
	} else if _, err := os.Stat(PrevPath()); err == nil {
		in.Prev = Version(PrevPath())
	}
	out, _ := env.Run(3*time.Second, "uci", "-q", "get", "toofanwrt.main.xray_pinned")
	in.Pinned = strings.TrimSpace(out) == "1"
	return in
}
