//go:build integration

package update

// End-to-end check of Apply + guard.sh on a throwaway machine (it writes
// /usr/bin/separ, /etc/init.d/separ and /etc/separ). Run with:
//   OLD_BIN=... NEW_BIN=... go test -tags integration ./internal/update/

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeInit = `#!/bin/sh
case "$1" in
stop) [ -f /tmp/fake-separ.pid ] && kill $(cat /tmp/fake-separ.pid) 2>/dev/null; sleep 1 ;;
start) setsid /usr/bin/separ -mock -listen 127.0.0.1:18080 -config /tmp/fake-separ.json >>/tmp/fake-separ.log 2>&1 & echo $! > /tmp/fake-separ.pid ;;
esac
`

const fakeInstall = `#!/bin/sh
cd "$(dirname "$0")"
/etc/init.d/separ stop
cp bin/separ-amd64 /usr/bin/separ
/etc/init.d/separ start
`

func release(t *testing.T, bin string) string {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "separ", "bin"), 0o755)
	b, _ := os.ReadFile(bin)
	os.WriteFile(filepath.Join(dir, "separ", "bin", "separ-amd64"), b, 0o755)
	os.WriteFile(filepath.Join(dir, "separ", "install.sh"), []byte(fakeInstall), 0o755)
	out := filepath.Join(t.TempDir(), "r.tar.gz")
	if o, err := exec.Command("tar", "-czf", out, "-C", dir, "separ").CombinedOutput(); err != nil {
		t.Fatal(string(o))
	}
	return out
}

func pingVersion() string {
	resp, err := http.Get("http://127.0.0.1:18080/api/ping")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var d struct{ Version string }
	b, _ := io.ReadAll(resp.Body)
	json.Unmarshal(b, &d)
	return d.Version
}

func waitVersion(want string, d time.Duration) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if pingVersion() == want {
			return true
		}
		time.Sleep(time.Second)
	}
	return false
}

func lastResult(t *testing.T) Result {
	var r Result
	b, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(b, &r)
	return r
}

func TestUpdateAndRollback(t *testing.T) {
	oldBin, newBin := os.Getenv("OLD_BIN"), os.Getenv("NEW_BIN")
	if oldBin == "" || newBin == "" {
		t.Skip("OLD_BIN/NEW_BIN not set")
	}
	os.MkdirAll("/etc/separ", 0o700)
	os.MkdirAll("/etc/init.d", 0o755)
	os.WriteFile("/etc/init.d/separ", []byte(fakeInit), 0o755)
	b, _ := os.ReadFile(oldBin)
	os.WriteFile("/usr/bin/separ", b, 0o755)
	exec.Command("/etc/init.d/separ", "start").Run()
	defer exec.Command("/etc/init.d/separ", "stop").Run()
	if !waitVersion("v-old", 10*time.Second) {
		t.Fatal("old version did not start")
	}

	// 1) good update
	u := &Updater{Version: "v-old", Port: 18080}
	if err := u.Apply(release(t, newBin)); err != nil {
		t.Fatal(err)
	}
	if !waitVersion("v-new", 60*time.Second) {
		t.Fatal("new version not running")
	}
	time.Sleep(3 * time.Second)
	if r := lastResult(t); !r.OK || r.To != "v-new" {
		t.Fatalf("result %+v", r)
	}

	// 2) broken update: answers -version but never serves; must roll back
	broken := filepath.Join(t.TempDir(), "broken")
	os.WriteFile(broken, []byte("#!/bin/sh\n[ \"$1\" = -version ] && echo v-broken && exit 0\nexit 1\n"), 0o755)
	u = &Updater{Version: "v-new", Port: 18080}
	if err := u.Apply(release(t, broken)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Second)
	if !waitVersion("v-new", 120*time.Second) {
		t.Fatal("rollback did not restore v-new")
	}
	time.Sleep(2 * time.Second)
	if r := lastResult(t); r.OK || !strings.Contains(r.Error, "rolled back") {
		t.Fatalf("result %+v", r)
	}
}
