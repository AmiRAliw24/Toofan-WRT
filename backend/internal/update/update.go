// Package update installs a new panel release from a tarball, with a
// detached guard that rolls back if the new version doesn't come up.
package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"separ/internal/env"
)

const (
	workDir    = "/tmp/separ-update"
	resultFile = "/etc/separ/update-result.json"
)

type Result struct {
	OK    bool   `json:"ok"`
	From  string `json:"from"`
	To    string `json:"to"`
	Error string `json:"error,omitempty"`
	At    int64  `json:"at"`
}

type Manifest struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Notes   string `json:"notes"`
}

type Status struct {
	State   string  `json:"state"` // idle | downloading | verifying | installing | failed
	Error   string  `json:"error,omitempty"`
	Target  string  `json:"target,omitempty"`
	Current string  `json:"current"`
	Last    *Result `json:"last,omitempty"`
}

type Updater struct {
	Version   string
	Port      int
	SocksPort int  // used to download through the tunnel when direct fails
	Simulate  bool // demo mode: pretend instead of installing

	mu    sync.Mutex
	state Status
}

func (u *Updater) Status() Status {
	u.mu.Lock()
	st := u.state
	u.mu.Unlock()
	if st.State == "" {
		st.State = "idle"
	}
	st.Current = u.Version
	if b, err := os.ReadFile(resultFile); err == nil {
		var r Result
		if json.Unmarshal(b, &r) == nil {
			st.Last = &r
		}
	}
	return st
}

func (u *Updater) set(state, errMsg, target string) {
	u.mu.Lock()
	u.state = Status{State: state, Error: errMsg, Target: target}
	u.mu.Unlock()
}

func (u *Updater) Busy() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := u.state.State
	return s == "downloading" || s == "verifying" || s == "installing"
}

func archBin() string {
	switch runtime.GOARCH {
	case "arm":
		return "separ-armv7"
	case "arm64":
		return "separ-arm64"
	case "mipsle":
		return "separ-mipsle"
	}
	return "separ-amd64"
}

// Apply verifies a release tarball and hands installation to the guard.
func (u *Updater) Apply(tarball string) error {
	u.set("verifying", "", "")
	fail := func(err error) error {
		u.set("failed", err.Error(), "")
		return err
	}
	if u.Simulate {
		time.Sleep(time.Second)
		u.set("installing", "", "mock")
		return nil
	}
	os.RemoveAll(filepath.Join(workDir, "separ"))
	os.MkdirAll(workDir, 0o700)
	if _, err := env.Run(60*time.Second, "tar", "-xzf", tarball, "-C", workDir); err != nil {
		return fail(errors.New("not a valid release file"))
	}
	os.Remove(tarball)
	dir := filepath.Join(workDir, "separ")
	bin := filepath.Join(dir, "bin", archBin())
	if _, err := os.Stat(filepath.Join(dir, "install.sh")); err != nil {
		return fail(errors.New("release file has no install.sh"))
	}
	os.Chmod(bin, 0o755)
	out, err := env.Run(10*time.Second, bin, "-version")
	if err != nil || out == "" {
		return fail(errors.New("new binary does not run on this router"))
	}
	newVer := strings.TrimSpace(out)
	if newVer == u.Version {
		return fail(fmt.Errorf("version %s is already installed", newVer))
	}
	guard := filepath.Join(workDir, "guard.sh")
	if err := os.WriteFile(guard, []byte(guardScript), 0o700); err != nil {
		return fail(err)
	}
	logf, _ := os.Create(filepath.Join(workDir, "update.log"))
	cmd := exec.Command("/bin/sh", guard, newVer, u.Version, fmt.Sprint(u.Port), workDir)
	cmd.Stdout, cmd.Stderr = logf, logf
	// Own session: the guard must outlive this process, which it restarts.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fail(err)
	}
	cmd.Process.Release()
	u.set("installing", "", newVer)
	return nil
}

// guardScript installs the release, waits for the new version to answer,
// and restores the previous binary if it doesn't within 90 seconds.
const guardScript = `#!/bin/sh
NEW="$1"; OLD="$2"; PORT="$3"; DIR="$4"
sleep 1
cp /usr/bin/separ /usr/bin/separ.prev
touch /tmp/separ-restarting
( cd "$DIR/separ" && SEPAR_UPDATE=1 sh install.sh )
# The new version may listen elsewhere (its installer can change the port).
P="$(uci -q get separ.main.listen | cut -d, -f1 | sed 's/.*://')"
[ -n "$P" ] && PORT="$P"
ok=0; i=0
while [ $i -lt 45 ]; do
	sleep 2; i=$((i+1))
	if wget -q -O - "http://127.0.0.1:$PORT/api/ping" 2>/dev/null | grep -q "\"version\":\"$NEW\""; then ok=1; break; fi
done
if [ $ok = 1 ]; then
	echo "{\"ok\":true,\"from\":\"$OLD\",\"to\":\"$NEW\",\"at\":$(date +%s)}" > /etc/separ/update-result.json
else
	touch /tmp/separ-restarting
	/etc/init.d/separ stop
	sleep 3
	cp /usr/bin/separ.prev /usr/bin/separ.new && mv -f /usr/bin/separ.new /usr/bin/separ
	/etc/init.d/separ start
	echo "{\"ok\":false,\"from\":\"$OLD\",\"to\":\"$NEW\",\"error\":\"new version did not start; rolled back\",\"at\":$(date +%s)}" > /etc/separ/update-result.json
fi
cp "$DIR/update.log" /etc/separ/update.log 2>/dev/null
rm -rf "$DIR/separ"
`

func (u *Updater) client(viaTunnel bool) *http.Client {
	tr := &http.Transport{}
	if viaTunnel && u.SocksPort > 0 {
		pu, _ := url.Parse(fmt.Sprintf("socks5h://127.0.0.1:%d", u.SocksPort))
		tr.Proxy = http.ProxyURL(pu)
	}
	return &http.Client{Transport: tr, Timeout: 5 * time.Minute}
}

func (u *Updater) get(rawURL string) (*http.Response, error) {
	var lastErr error
	for _, tunnel := range []bool{false, true} {
		resp, err := u.client(tunnel).Get(rawURL)
		if err == nil && resp.StatusCode == 200 {
			return resp, nil
		}
		if err == nil {
			resp.Body.Close()
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		lastErr = err
	}
	return nil, lastErr
}

// Check fetches the release manifest.
func (u *Updater) Check(manifestURL string) (Manifest, error) {
	var m Manifest
	if u.Simulate {
		return Manifest{Version: "2026.11.01-mock", Notes: "demo"}, nil
	}
	if manifestURL == "" {
		return m, errors.New("no update source configured")
	}
	resp, err := u.get(manifestURL)
	if err != nil {
		return m, err
	}
	defer resp.Body.Close()
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&m)
	if err == nil && (m.Version == "" || m.URL == "") {
		err = errors.New("invalid manifest")
	}
	return m, err
}

// Install downloads the release in the manifest, checks its hash and applies it.
func (u *Updater) Install(m Manifest) error {
	u.set("downloading", "", m.Version)
	if u.Simulate {
		return u.Apply("")
	}
	resp, err := u.get(m.URL)
	if err != nil {
		u.set("failed", err.Error(), m.Version)
		return err
	}
	defer resp.Body.Close()
	os.MkdirAll(workDir, 0o700)
	path := filepath.Join(workDir, "release.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		u.set("failed", err.Error(), m.Version)
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 128<<20))
	f.Close()
	if err == nil && m.SHA256 != "" && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), m.SHA256) {
		err = errors.New("checksum mismatch")
	}
	if err != nil {
		os.Remove(path)
		u.set("failed", err.Error(), m.Version)
		return err
	}
	return u.Apply(path)
}
