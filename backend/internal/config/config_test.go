package config

import (
	"os"
	"path/filepath"
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
