package fw

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func samplePlan() Plan {
	return Plan{
		LANIfaces: []string{"br-lan"}, AllIfaces: []string{"br-lan", "br-guest"},
		TProxy: true, TProxyPort: 12345,
		IranCIDRs:  []string{"2.57.3.0/24", "5.22.0.0/17"},
		BypassMACs: []string{"00:1e:c2:7d:44:90"},
		BlockMACs:  []string{"f4:5c:89:aa:01:77"},
		BlockIPv6:  true,
		Devices: []DeviceRule{
			{MAC: "de:41:7a:0b:33:12", IP: "192.168.1.123", DownKbps: 8000, UpKbps: 2000},
			{MAC: "8a:2f:11:42:9c:01", IP: "192.168.1.238"},
		},
		Schedules:   []ScheduleRule{{MACs: []string{"8a:2f:11:42:9c:01"}, Days: []int{0, 1, 2, 3, 4}, From: "23:00", To: "07:00"}},
		PriorityIPs: []string{"192.168.1.140"},
		LockWAN:     true, WANDev: "wan", PanelPorts: []int{8080, 80},
		DirectNetdevs: []string{"phy1-ap1", "phy0-ap1"},
	}
}

func TestRenderContainsRules(t *testing.T) {
	s := Render(samplePlan())
	for _, want := range []string{
		"tproxy ip to 127.0.0.1:12345",
		"ip daddr @iran4 return",
		`meta hour "23:00"-"23:59:59"`,
		`meta day { 1, 2, 3, 4, 5 } meta hour "00:00"-"07:00"`,
		"limit rate over 1000 kbytes/second",
		`comment "d 192.168.1.238"`,
		"reject with icmpv6 type admin-prohibited",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

// TestNftSyntax asks the real nft to parse the script (needs CAP_NET_ADMIN).
func TestNftSyntax(t *testing.T) {
	if os.Getenv("NFT_CHECK") == "" {
		t.Skip("set NFT_CHECK=1 to validate with nft -c")
	}
	blocked := samplePlan()
	blocked.IranBlock = true
	blockedOff := blocked
	blockedOff.TProxy = false
	for _, p := range []Plan{samplePlan(), blocked, blockedOff, {}, {LANIfaces: []string{"br-lan"}}} {
		cmd := exec.Command("nft", "-c", "-f", "-")
		cmd.Stdin = strings.NewReader(Render(p) + RenderBridge(p))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("nft rejected: %v\n%s\n%s", err, out, Render(p))
		}
	}
}

func TestRenderIranBlock(t *testing.T) {
	p := samplePlan()
	p.IranBlock = true
	p.TProxy = false // still refused while the tunnel is down
	s := Render(p)
	for _, want := range []string{
		"ip daddr @iran4 meta mark set meta mark | 0x200 return",
		"meta mark & 0x200 == 0x200 meta l4proto tcp reject with tcp reset",
		"ether saddr @bypass_mac return",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "tproxy ip") {
		t.Error("no capture while the tunnel is down")
	}
}
