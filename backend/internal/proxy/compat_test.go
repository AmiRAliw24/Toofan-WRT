package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"toofanwrt/internal/config"
)

func TestAllowInsecureBecomesPin(t *testing.T) {
	srv := httptest.NewTLSServer(nil)
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	host, portS, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portS)
	old := XrayVersion
	defer func() { XrayVersion = old }()

	link := "vless://2b3c1f6e-8d1a-4c7e-9f00-123456789abc@" + host + ":" + portS + "?type=ws&security=tls&sni=example.com&allowInsecure=1#self"
	s, err := ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	XrayVersion = [3]int{25, 1, 30}
	ob, err := ProxyOutbound(s)
	if err != nil || ob.StreamSettings.TLSSettings["allowInsecure"] != true {
		t.Fatalf("25.x should keep allowInsecure: %v", err)
	}
	XrayVersion = [3]int{26, 3, 27}
	ob, err = ProxyOutbound(s)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(srv.Certificate().Raw)
	if ob.StreamSettings.TLSSettings["pinnedPeerCertSha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("pin %v", ob.StreamSettings.TLSSettings)
	}
	if _, ok := ob.StreamSettings.TLSSettings["allowInsecure"]; ok {
		t.Fatal("allowInsecure left in place")
	}
	_ = port
}

func TestNeedsCompatEngine(t *testing.T) {
	for _, c := range []struct {
		s    config.Server
		want bool
	}{
		{config.Server{Protocol: "vless", Security: "none", Address: "de.example.com"}, true},
		{config.Server{Protocol: "vless", Security: "", Address: "1.2.3.4"}, true},
		{config.Server{Protocol: "trojan", Security: "none", Address: "x.example.com"}, true},
		{config.Server{Protocol: "vless", Security: "tls", Address: "de.example.com"}, false},
		{config.Server{Protocol: "vless", Security: "reality", Address: "de.example.com"}, false},
		{config.Server{Protocol: "vless", Security: "none", Address: "192.168.1.20"}, false},
		{config.Server{Protocol: "vmess", Security: "none", Address: "de.example.com"}, false},
		{config.Server{Protocol: "shadowsocks", Address: "de.example.com"}, false},
	} {
		if got := NeedsCompatEngine(c.s); got != c.want {
			t.Errorf("NeedsCompatEngine(%+v) = %v, want %v", c.s, got, c.want)
		}
	}
}
