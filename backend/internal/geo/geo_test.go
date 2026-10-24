package geo

import (
	"os"
	"testing"
)

func TestCountryCIDRs(t *testing.T) {
	p := os.Getenv("GEOIP_DAT")
	if p == "" {
		t.Skip("set GEOIP_DAT to a geoip.dat file")
	}
	got, err := CountryCIDRs(p, "ir")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 100 {
		t.Fatalf("only %d CIDRs", len(got))
	}
	t.Logf("%d CIDRs, first %s", len(got), got[0])
}
