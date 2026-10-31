package system

import "testing"

func TestPickTemp(t *testing.T) {
	if c, n := pickTemp([]Temp{{"ath10k_hwmon (phy0)", 47}, {"ath10k_hwmon (phy1)", 51}}); c != 51 || n != "ath10k_hwmon (phy1)" {
		t.Errorf("hottest radio expected, got %v %q", c, n)
	}
	if c, n := pickTemp([]Temp{{"ath10k_hwmon (phy0)", 60}, {"cpu-thermal", 48}}); c != 48 || n != "cpu-thermal" {
		t.Errorf("cpu sensor expected, got %v %q", c, n)
	}
	if c, n := pickTemp(nil); c != 0 || n != "" {
		t.Errorf("no sensor: got %v %q", c, n)
	}
}

func TestParseTZ(t *testing.T) {
	for in, want := range map[string]int{"+0330": 210, "-0500": -300, "+0000": 0, "bad": 0, "": 0} {
		if got := parseTZ(in); got != want {
			t.Errorf("parseTZ(%q) = %d, want %d", in, got, want)
		}
	}
}
