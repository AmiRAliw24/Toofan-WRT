package proxy

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"separ/internal/config"
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
	for i, s := range servers {
		for _, split := range []bool{true, false} {
			data, err := BuildConfig(BuildOpts{Server: s, SplitIran: split, BlockAds: true, HaveIran: true,
				ISPDNS: []string{"192.168.1.1", "10.10.34.34"}})
			if err != nil {
				t.Fatal(err)
			}
			check(s.Protocol+"-"+s.Network+"-"+string(rune('a'+i))+map[bool]string{true: "-split", false: ""}[split], data)
		}
	}
	data, _, err := BuildTestConfig(servers, 21000, []string{"192.168.1.1"})
	if err != nil {
		t.Fatal(err)
	}
	check("tester", data)
	_ = config.Server{}
}
