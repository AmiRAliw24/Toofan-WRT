package proxy

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"toofanwrt/internal/config"
)

var sampleLinks = []string{
	"vless://2b3c1f6e-8d1a-4c7e-9f00-123456789abc@de1.example.net:443?type=xhttp&security=reality&pbk=Q2hhbmdlTWVQbGVhc2VDaGFuZ2VNZVBsZWFzZTEyMzQ&sni=www.microsoft.com&fp=chrome&sid=ab12&path=%2Fapi&mode=auto#%F0%9F%87%A9%F0%9F%87%AA%20Germany",
	"vless://2b3c1f6e-8d1a-4c7e-9f00-123456789abc@1.2.3.4:443?type=tcp&security=reality&pbk=Q2hhbmdlTWVQbGVhc2VDaGFuZ2VNZVBsZWFzZTEyMzQ&sni=www.speedtest.net&fp=chrome&sid=ab12&flow=xtls-rprx-vision#Vision",
	"vless://2b3c1f6e-8d1a-4c7e-9f00-123456789abc@cdn.example.com:443?type=ws&security=tls&sni=cdn.example.com&host=cdn.example.com&path=%2Fws%3Fed%3D2048&alpn=h2%2Chttp%2F1.1&fp=chrome#WS-TLS",
	"vless://2b3c1f6e-8d1a-4c7e-9f00-123456789abc@grpc.example.com:443?type=grpc&security=tls&serviceName=grpcsvc&mode=multi#gRPC",
	"vless://2b3c1f6e-8d1a-4c7e-9f00-123456789abc@hu.example.com:80?type=httpupgrade&security=none&path=%2Fup&host=hu.example.com#HU",
	"trojan://secretpass@tr.example.com:443?security=tls&sni=tr.example.com&type=tcp#Trojan",
	"ss://" + base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:sspass")) + "@5.6.7.8:8388#SS",
	"vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"VMess WS","add":"vm.example.com","port":"443","id":"2b3c1f6e-8d1a-4c7e-9f00-123456789abc","aid":"0","net":"ws","type":"none","host":"vm.example.com","path":"/vm","tls":"tls","sni":"vm.example.com"}`)),
}

func TestParseLinks(t *testing.T) {
	servers, bad := ParseLinks(strings.Join(sampleLinks, "\n") + "\nnot-a-link\nvless://broken")
	if len(servers) != len(sampleLinks) {
		t.Fatalf("parsed %d of %d, bad=%v", len(servers), len(sampleLinks), bad)
	}
	if len(bad) != 2 {
		t.Fatalf("expected 2 bad lines, got %v", bad)
	}
	if servers[0].Name != "🇩🇪 Germany" || servers[0].Network != "xhttp" || servers[0].Security != "reality" {
		t.Fatalf("unexpected first server: %+v", servers[0])
	}
	if servers[7].Name != "VMess WS" || servers[7].Protocol != "vmess" {
		t.Fatalf("vmess: %+v", servers[7])
	}
}

func TestParseBase64Subscription(t *testing.T) {
	body := base64.StdEncoding.EncodeToString([]byte(strings.Join(sampleLinks[:3], "\n")))
	servers, _ := ParseLinks(body)
	if len(servers) != 3 {
		t.Fatalf("got %d servers", len(servers))
	}
}

func TestVisionFlow(t *testing.T) {
	ob, _, err := Outbound(sampleLinks[1])
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(ob)
	if !strings.Contains(string(b), `"flow":"xtls-rprx-vision"`) || !strings.Contains(string(b), `"network":"raw"`) {
		t.Fatal(string(b))
	}
}

// TestXrayAcceptsConfigs runs "xray run -test" on generated configs when
// XRAY_BIN and XRAY_ASSETS point at a real binary and geo files.
func TestXrayAcceptsConfigs(t *testing.T) {
	bin, assets := os.Getenv("XRAY_BIN"), os.Getenv("XRAY_ASSETS")
	if bin == "" || assets == "" {
		t.Skip("XRAY_BIN / XRAY_ASSETS not set")
	}
	// Configs depend on the running version (pins, nonIPQuery): use the real one.
	old := XrayVersion
	defer func() { XrayVersion = old }()
	if DetectXrayVersion(bin) == "" {
		t.Fatal("could not read the Xray version")
	}
	t.Logf("xray %v", XrayVersion)
	dir := t.TempDir()
	check := func(name string, data []byte) {
		p := filepath.Join(dir, name+".json")
		os.WriteFile(p, data, 0o600)
		cmd := exec.Command(bin, "run", "-test", "-c", p)
		cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+assets)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("%s rejected: %v\n%s", name, err, out)
		}
	}
	servers, _ := ParseLinks(strings.Join(sampleLinks, "\n"))
	wg, _ := ParseLinks(sampleWG)
	_, ovpn, _ := ImportOpenVPN("remote vpn.example.com 1194\n<ca>\nx\n</ca>\n", "", "", "")
	servers = append(servers, wg[0], ovpn)
	// Releases after 26.3.27 refuse VLESS/Trojan without TLS: the panel runs
	// those servers on the compat engine, so this binary must refuse them
	// and everything else must load.
	newer := XrayVersion[0] > 26 || (XrayVersion[0] == 26 && XrayVersion[1] > 3)
	var main []config.Server
	for _, s := range servers {
		if newer && NeedsCompatEngine(s) {
			data, _, _ := BuildConfig(BuildOpts{Server: s, IranMode: config.IranDirect})
			p := filepath.Join(dir, "compat-only.json")
			os.WriteFile(p, data, 0o600)
			if exec.Command(bin, "run", "-test", "-c", p).Run() == nil {
				t.Logf("%s %s is accepted by this release; the compat engine may no longer be needed", s.Protocol, s.Name)
			}
			continue
		}
		main = append(main, s)
	}
	servers = main
	for i, s := range servers {
		for j, mode := range []string{config.IranDirect, config.IranTunnel, config.IranBlock, config.IranBlock} {
			kernel := j < 3 // last case: no kernel set, Xray matches geoip itself
			data, _, err := BuildConfig(BuildOpts{Server: s, IranMode: mode, BlockAds: true, HaveIran: true, HaveGeoIP: true,
				ISPDNS: []string{"192.168.1.1", "10.10.34.34"}, KernelIran: kernel, AllHosts: []string{"other.example.com"}})
			if err != nil {
				t.Fatal(err)
			}
			if mode != config.IranTunnel && !strings.Contains(string(data), `"ext:iran.dat:other"`) {
				// "ir" only lists .ir names; digikala.com and co. are in "other".
				t.Fatal(mode + " mode must route Iranian non-.ir domains")
			}
			check(fmt.Sprintf("%s-%s-%c-%s-%d", s.Protocol, s.Network, 'a'+i, mode, j), data)
		}
	}
	data, _, _, err := BuildTestConfig(servers, 21000, []string{"192.168.1.1"})
	if err != nil {
		t.Fatal(err)
	}
	check("tester", data)
	_ = config.Server{}
}

const sampleWG = `[Interface]
PrivateKey = yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=
Address = 172.16.0.2/32, 2606:4700:110:8a36::2/128
DNS = 1.1.1.1
MTU = 1280

[Peer]
PublicKey = bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = engage.cloudflareclient.com:2408
PersistentKeepalive = 25
`

func TestWireGuardConf(t *testing.T) {
	servers, bad := ParseLinks(sampleWG)
	if len(servers) != 1 || len(bad) != 0 {
		t.Fatalf("servers=%v bad=%v", servers, bad)
	}
	s := servers[0]
	if s.Protocol != "wireguard" || s.Address != "engage.cloudflareclient.com" || s.Port != 2408 {
		t.Fatalf("%+v", s)
	}
	ob, _, err := Outbound(s.Link)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(ob)
	for _, want := range []string{`"secretKey":"yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="`, `"keepAlive":25`, `"mtu":1280`, `"172.16.0.2/32"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
}

func TestOpenVPNImport(t *testing.T) {
	conf := "client\ndev tun\nproto udp\nremote vpn.example.com 1194\nredirect-gateway def1\n<ca>\n-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n</ca>\n"
	prof, srv, err := ImportOpenVPN(conf, "", "u", "p")
	if err != nil {
		t.Fatal(err)
	}
	if srv.Address != "vpn.example.com" || srv.Port != 1194 || prof.ID != srv.ID || !IsOpenVPN(srv) {
		t.Fatalf("%+v %+v", prof, srv)
	}
	if strings.Contains(sanitizeOVPN(conf), "redirect-gateway") {
		t.Fatal("redirect-gateway not removed")
	}
	ob, _, err := Outbound(srv.Link)
	if err != nil || ob.Protocol != "freedom" {
		t.Fatal(err, ob)
	}
}

func TestBaseFingerprintIgnoresServer(t *testing.T) {
	a, _ := ParseLink(sampleLinks[0])
	b, _ := ParseLink(sampleLinks[1])
	hosts := []string{a.Address, b.Address}
	_, fa, _ := BuildConfig(BuildOpts{Server: a, IranMode: config.IranDirect, AllHosts: hosts})
	_, fb, _ := BuildConfig(BuildOpts{Server: b, IranMode: config.IranDirect, AllHosts: hosts})
	if fa != fb {
		t.Fatal("switching servers must not change the base config")
	}
	_, fc, _ := BuildConfig(BuildOpts{Server: b, IranMode: config.IranBlock, AllHosts: hosts})
	if fc == fb {
		t.Fatal("changing split must change the base config")
	}
}

// Xray refuses to start when a rule needs a geo file that isn't there.
func TestNoGeoIPRuleWithoutGeoIP(t *testing.T) {
	s, _ := ParseLink(sampleLinks[0])
	data, _, err := BuildConfig(BuildOpts{Server: s, IranMode: config.IranDirect, HaveIran: true, HaveGeoIP: false})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "geoip:ir") {
		t.Fatal("geoip:ir used although geoip.dat is missing")
	}
	data, _, _ = BuildConfig(BuildOpts{Server: s, IranMode: config.IranDirect, HaveIran: true, HaveGeoIP: true})
	if !strings.Contains(string(data), "geoip:ir") {
		t.Fatal("geoip:ir missing although geoip.dat is installed and there is no kernel set")
	}
}
