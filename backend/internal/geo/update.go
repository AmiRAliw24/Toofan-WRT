package geo

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"toofanwrt/internal/env"
	"toofanwrt/internal/fetch"
)

// Geo data from chocolate4u/Iran-v2ray-rules: everything in v2fly's lists
// plus Iranian categories (geosite:ir, category-ads-all, malware, …) and
// a fuller geoip:ir. Published daily with SHA-256 files. Kept on flash in
// Dir; until the first download the packaged files are used.
const Source = "chocolate4u/Iran-v2ray-rules"

var (
	Dir     = "/usr/share/toofanwrt/geo"
	baseURL = "https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download/"
)

var geoFiles = []string{"geosite.dat", "geoip.dat"}

type FileMeta struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Meta struct {
	Source    string              `json:"source"`
	Tag       string              `json:"tag"` // release, e.g. 202610311033
	UpdatedAt int64               `json:"updatedAt"`
	Files     map[string]FileMeta `json:"files"`
}

// Job is the state of a running or finished update.
type Job struct {
	Running bool   `json:"running"`
	Stage   string `json:"stage,omitempty"` // download geosite.dat | download geoip.dat | verify | done
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

// Downloaded reports whether the chocolate4u files are installed.
func Downloaded() bool {
	if env.Mock {
		return true
	}
	for _, f := range geoFiles {
		if FirstExisting([]string{filepath.Join(Dir, f)}) == "" {
			return false
		}
	}
	return true
}

func metaPath() string { return filepath.Join(Dir, "meta.json") }

var mockMeta Meta

func LoadMeta() Meta {
	if env.Mock {
		if mockMeta.Tag == "" {
			return Meta{Source: Source, Tag: "202610311033", UpdatedAt: time.Now().Add(-26 * time.Hour).Unix(),
				Files: map[string]FileMeta{"geosite.dat": {Size: 8339874}, "geoip.dat": {Size: 16512274}}}
		}
		return mockMeta
	}
	m := Meta{Source: Source}
	if b, err := os.ReadFile(metaPath()); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

var tagRe = regexp.MustCompile(`/download/([^/]+)/`)

// Update downloads both files, checks them against the published SHA-256
// and that they hold Iranian data, then swaps them in (the old ones are
// kept as .prev for Rollback).
func Update() (Meta, error) {
	jobMu.Lock()
	if job.Running {
		jobMu.Unlock()
		return Meta{}, errors.New("an update is already running")
	}
	job = Job{Running: true, Stage: "start"}
	jobMu.Unlock()
	m, err := update()
	setJob(func(j *Job) {
		j.Running, j.At = false, time.Now().Unix()
		if err != nil {
			j.Error, j.Stage = err.Error(), "failed"
		} else {
			j.Error, j.Stage = "", "done"
		}
	})
	return m, err
}

func update() (Meta, error) {
	if env.Mock {
		for _, st := range []string{"download geosite.dat", "download geoip.dat", "verify"} {
			setJob(func(j *Job) { j.Stage, j.Done, j.Total = st, 8<<20, 16<<20 })
			time.Sleep(700 * time.Millisecond)
		}
		mockMeta = Meta{Source: Source, Tag: time.Now().Format("200601021504"), UpdatedAt: time.Now().Unix(),
			Files: map[string]FileMeta{"geosite.dat": {Size: 8339874}, "geoip.dat": {Size: 16512274}}}
		return mockMeta, nil
	}
	if err := os.MkdirAll(Dir, 0o755); err != nil {
		return Meta{}, err
	}
	m := Meta{Source: Source, Files: map[string]FileMeta{}}
	for _, f := range geoFiles {
		name := f
		setJob(func(j *Job) { j.Stage, j.Done, j.Total = "download "+name, 0, 0 })
		sumText, err := fetch.Text(baseURL + f + ".sha256sum")
		if err != nil {
			return m, fmt.Errorf("%s checksum: %w", f, err)
		}
		want := fetch.FindSHA256(sumText)
		if want == "" {
			return m, fmt.Errorf("%s: no checksum published", f)
		}
		tmp := filepath.Join(Dir, f+".new")
		got, final, err := fetch.ToFile(baseURL+f, tmp, 96<<20, func(done, total int64) {
			setJob(func(j *Job) { j.Done, j.Total = done, total })
		})
		if err != nil {
			return m, fmt.Errorf("%s: %w", f, err)
		}
		if got != want {
			os.Remove(tmp)
			return m, fmt.Errorf("%s: checksum mismatch", f)
		}
		if t := tagRe.FindStringSubmatch(final); t != nil {
			m.Tag = t[1]
		}
		st, _ := os.Stat(tmp)
		m.Files[f] = FileMeta{Size: st.Size(), SHA256: got}
	}
	setJob(func(j *Job) { j.Stage = "verify" })
	if cidrs, err := CountryCIDRs(filepath.Join(Dir, "geoip.dat.new"), "ir"); err != nil || len(cidrs) < 500 {
		cleanupNew()
		return m, errors.New("geoip.dat has no usable Iranian ranges")
	}
	if n, err := SiteCount(filepath.Join(Dir, "geosite.dat.new"), "ir"); err != nil || n < 1000 {
		cleanupNew()
		return m, errors.New("geosite.dat has no usable Iranian domain list")
	}
	for _, f := range geoFiles {
		cur := filepath.Join(Dir, f)
		if _, err := os.Stat(cur); err == nil {
			os.Rename(cur, cur+".prev")
		}
		if err := os.Rename(cur+".new", cur); err != nil {
			return m, err
		}
	}
	m.UpdatedAt = time.Now().Unix()
	b, _ := json.MarshalIndent(m, "", "  ")
	os.WriteFile(metaPath(), b, 0o644)
	return m, nil
}

func cleanupNew() {
	for _, f := range geoFiles {
		os.Remove(filepath.Join(Dir, f+".new"))
	}
}

// Rollback restores the files from before the last update.
func Rollback() error {
	for _, f := range geoFiles {
		cur := filepath.Join(Dir, f)
		if _, err := os.Stat(cur + ".prev"); err != nil {
			return errors.New("no previous geo files")
		}
		if err := os.Rename(cur+".prev", cur); err != nil {
			return err
		}
	}
	os.Remove(metaPath())
	return nil
}

// SiteCount returns how many domains a geosite category lists.
func SiteCount(path, code string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	code = strings.ToUpper(code)
	n := -1
	err = eachField(data, func(num int, wire int, val []byte, _ uint64) error {
		if num != 1 || wire != 2 {
			return nil
		}
		var cc string
		count := 0
		eachField(val, func(fn, w int, v []byte, _ uint64) error {
			switch {
			case fn == 1 && w == 2:
				cc = string(v)
			case fn == 2 && w == 2:
				count++
			}
			return nil
		})
		if strings.ToUpper(cc) == code {
			n = count
			return errStop
		}
		return nil
	})
	if err != nil && err != errStop {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("category %s not found", code)
	}
	return n, nil
}
