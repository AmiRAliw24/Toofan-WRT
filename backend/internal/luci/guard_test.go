package luci

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestUhttpdOnOurPorts(t *testing.T) {
	conf := map[string]string{
		"uhttpd.main":              "uhttpd",
		"uhttpd.main.listen_http":  "0.0.0.0:80 [::]:80",
		"uhttpd.main.listen_https": "0.0.0.0:443 [::]:443",
		"uhttpd.alt.listen_http":   "0.0.0.0:8081",
		"uhttpd.x.listen_https":    "443",
		"uhttpd.main.home":         "/www",
	}
	got := uhttpdOnOurPorts(conf)
	sort.Strings(got)
	if len(got) != 2 || got[0] != "main" || got[1] != "x" {
		t.Fatalf("got %v", got)
	}
	if len(uhttpdOnOurPorts(map[string]string{"uhttpd.main.listen_http": "0.0.0.0:8081"})) != 0 {
		t.Fatal("the side port is fine")
	}
}

// PortHolder parses /proc/net/tcp and maps the socket inode to a process.
func TestPortHolder(t *testing.T) {
	dir := t.TempDir()
	procDir = dir
	defer func() { procDir = "/proc" }()
	os.MkdirAll(filepath.Join(dir, "net"), 0o755)
	os.WriteFile(filepath.Join(dir, "net/tcp"), []byte(
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"+
			"   0: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 4242 1 0 100 0 0 10 0\n"+
			"   1: 0100007F:1F91 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 777 1 0 100 0 0 10 0\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "1234/fd"), 0o755)
	os.Symlink("socket:[4242]", filepath.Join(dir, "1234/fd/5"))
	os.WriteFile(filepath.Join(dir, "1234/comm"), []byte("uhttpd\n"), 0o644)
	if pid, name := PortHolder(80); pid != 1234 || name != "uhttpd" {
		t.Fatalf("got %d %s", pid, name)
	}
	if pid, _ := PortHolder(443); pid != 0 {
		t.Fatal("443 is free")
	}
	if pid, name := PortHolder(8081); pid != -1 || name != "unknown" {
		t.Fatalf("listening socket without a known process: %d %s", pid, name)
	}
}
