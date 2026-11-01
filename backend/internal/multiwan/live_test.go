package multiwan

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"toofanwrt/internal/fw"
)

// TestLive runs inside a network namespace prepared by a script (see
// scratchpad run-mw.sh): it applies real routes, rules and nft rules.
// LIVE_LINKS="wan=up1=100.64.1.1,wwan=up2=100.64.2.1" LIVE_LAN=lan0 LIVE_TICKS=n
func TestLive(t *testing.T) {
	spec := os.Getenv("LIVE_LINKS")
	if spec == "" {
		t.Skip("needs a prepared namespace")
	}
	links := map[string][2]string{}
	for _, l := range strings.Split(spec, ",") {
		f := strings.Split(l, "=")
		links[f[0]] = [2]string{f[1], f[2]}
	}
	readIface = func(name string) (ifStatus, bool) {
		var st ifStatus
		l, ok := links[name]
		if !ok {
			return st, true
		}
		st.Up, st.L3Device = true, l[0]
		st.IPv4 = append(st.IPv4, struct {
			Address string `json:"address"`
		}{"x"})
		st.Route = append(st.Route, struct {
			Target  string `json:"target"`
			Nexthop string `json:"nexthop"`
		}{"0.0.0.0", l[1]})
		return st, true
	}
	m := New()
	m.Configure(true, map[string]int{"wan": 1, "wwan": 1})
	ticks := 1
	if os.Getenv("LIVE_TICKS") != "" {
		ticks = 3
	}
	for i := 0; i < ticks; i++ {
		m.tick()
	}
	plan := m.Plan()
	for _, l := range m.Status().Links {
		t.Logf("%s dev=%s up=%v healthy=%v rtt=%d share=%.2f", l.Name, l.Device, l.Up, l.Healthy, l.RTT, l.Share)
	}
	lan := os.Getenv("LIVE_LAN")
	script := fw.Render(fw.Plan{LANIfaces: []string{lan}, AllIfaces: []string{lan}, Balance: plan})
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nft: %v %s", err, out)
	}
	out, _ := exec.Command("ip", "rule").CombinedOutput()
	t.Logf("rules:\n%s", out)
}

func TestNextHealth(t *testing.T) {
	h, f, o := false, 0, 0
	// a new link counts on its first good probe
	if h, f, o = nextHealth(h, f, o, true, true, true); !h {
		t.Fatal("fresh link with a good probe is healthy")
	}
	// one or two lost probes don't take it out; the third does
	for i := 1; i <= 3; i++ {
		h, f, o = nextHealth(h, f, o, true, false, false)
		if want := i < 3; h != want {
			t.Fatalf("after %d failures healthy=%v", i, h)
		}
	}
	// back after two good probes in a row
	if h, f, o = nextHealth(h, f, o, true, true, false); h {
		t.Fatal("one good probe is not enough to come back")
	}
	if h, f, o = nextHealth(h, f, o, true, true, false); !h {
		t.Fatal("two good probes bring it back")
	}
	// interface down: out at once
	if h, _, _ = nextHealth(h, f, o, false, false, false); h {
		t.Fatal("down link")
	}
}

func TestPlanNeedsTwoLinks(t *testing.T) {
	m := New()
	m.Configure(true, map[string]int{"wan": 3})
	m.links["wan"].Up, m.links["wan"].Device, m.links["wan"].Healthy = true, "eth1", true
	if m.Plan() != nil {
		t.Fatal("one link: nothing to aggregate, the main table decides")
	}
	m.links["wwan"].Up, m.links["wwan"].Device, m.links["wwan"].Healthy = true, "wl-uplink", true
	p := m.Plan()
	if len(p) != 2 || p[0].Weight != 3 || p[0].Mark != 0x10000 || p[1].Mark != 0x20000 {
		t.Fatalf("plan %+v", p)
	}
	st := m.Status()
	if st.Active != 2 || st.Links[0].Share != 0.75 || st.Links[1].Share != 0.25 {
		t.Fatalf("status %+v", st)
	}
	m.Configure(false, nil)
	if m.Plan() != nil {
		t.Fatal("disabled")
	}
}
