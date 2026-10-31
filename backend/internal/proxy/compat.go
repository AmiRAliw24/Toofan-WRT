package proxy

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"toofanwrt/internal/config"

	"toofanwrt/internal/env"
)

// XrayVersion is detected once at start; config details differ by version.
var XrayVersion = [3]int{25, 1, 30}

var verRe = regexp.MustCompile(`Xray (\d+)\.(\d+)\.(\d+)`)

func DetectXrayVersion(bin string) string {
	if env.Mock {
		XrayVersion = [3]int{26, 3, 27}
		return "26.3.27"
	}
	out, err := env.Run(20*time.Second, bin, "version")
	if err != nil {
		return ""
	}
	m := verRe.FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	for i := 0; i < 3; i++ {
		XrayVersion[i], _ = strconv.Atoi(m[i+1])
	}
	return fmt.Sprintf("%d.%d.%d", XrayVersion[0], XrayVersion[1], XrayVersion[2])
}

var (
	pinMu    sync.Mutex
	pinCache = map[string]string{}
)

// certPin fetches the server's certificate and returns its SHA-256, the
// replacement Xray 26 requires for "allowInsecure" (self-signed servers).
func certPin(host string, port int, sni string) (string, error) {
	key := fmt.Sprintf("%s:%d/%s", host, port, sni)
	pinMu.Lock()
	if v, ok := pinCache[key]; ok {
		pinMu.Unlock()
		return v, nil
	}
	pinMu.Unlock()
	d := &net.Dialer{Timeout: 6 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", net.JoinHostPort(host, strconv.Itoa(port)), &tls.Config{
		ServerName: sni, InsecureSkipVerify: true, // we only read the certificate to pin it
	})
	if err != nil {
		return "", err
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", fmt.Errorf("no certificate")
	}
	sum := sha256.Sum256(certs[0].Raw)
	pin := hex.EncodeToString(sum[:])
	pinMu.Lock()
	pinCache[key] = pin
	pinMu.Unlock()
	return pin, nil
}

// NeedsCompatEngine reports whether a server only works on the compat
// engine: Xray releases after 26.3.27 refuse VLESS and Trojan without TLS
// to a public address.
func NeedsCompatEngine(s config.Server) bool {
	switch strings.ToLower(s.Protocol) {
	case "vless", "trojan":
	default:
		return false
	}
	if sec := strings.ToLower(s.Security); sec != "" && sec != "none" {
		return false
	}
	if ip := net.ParseIP(s.Address); ip != nil && (ip.IsPrivate() || ip.IsLoopback()) {
		return false
	}
	return true
}

// RefusesPlainVLESS reports whether an Xray version rejects VLESS/Trojan
// without TLS; 26.3.27 is the newest known to accept them.
func RefusesPlainVLESS(v [3]int) bool {
	last := [3]int{26, 3, 27}
	for i := 0; i < 3; i++ {
		if v[i] != last[i] {
			return v[i] > last[i]
		}
	}
	return false
}

// adaptOutbound rewrites settings that the running Xray version no longer
// accepts. Xray 26 removed "allowInsecure" in favour of a pinned certificate.
func adaptOutbound(ob *XOutbound) error {
	if XrayVersion[0] < 26 || ob.StreamSettings == nil || ob.StreamSettings.TLSSettings == nil {
		return nil
	}
	t := ob.StreamSettings.TLSSettings
	if insecure, _ := t["allowInsecure"].(bool); !insecure {
		return nil
	}
	delete(t, "allowInsecure")
	if env.Mock {
		t["pinnedPeerCertSha256"] = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		return nil
	}
	sni, _ := t["serverName"].(string)
	if sni == "" {
		sni = ob.addr
	}
	pin, err := certPin(ob.addr, ob.port, sni)
	if err != nil {
		return fmt.Errorf("insecure TLS server: could not read its certificate to pin it (%v)", err)
	}
	t["pinnedPeerCertSha256"] = pin
	return nil
}
