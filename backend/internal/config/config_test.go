package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Configs written by version 1 must still load.
func TestOpenV1Config(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte(`{"lang":"fa","proxy":{"enabled":true,"engine":"xray","wireguard":{"config":""},"openvpn":{"config":"","username":"","password":""},"servers":[]},"devices":{}}`), 0o600)
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Get().Proxy.Enabled {
		t.Fatal("lost settings")
	}
}

func TestNoNullLists(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(s.Get())
	if strings.Contains(string(b), "null") {
		t.Fatalf("null in %s", b)
	}
}
