package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
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
