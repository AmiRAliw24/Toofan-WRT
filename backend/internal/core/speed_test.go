package core

import "testing"

func TestPingStats(t *testing.T) {
	m, j := pingStats([]float64{20, 22, 21, 30, 20})
	if m != 21 || j != 6 { // diffs 2,1,9,10
		t.Fatalf("median %v jitter %v", m, j)
	}
}

func TestOoklaEndpoints(t *testing.T) {
	e := endpointsFor(SpeedServer{Kind: "ookla", Host: "sp.example.ir:8080", URL: "http://sp.example.ir:8080/speedtest/upload.php"})
	if e.legacyPing != "http://sp.example.ir:8080/speedtest/latency.txt" {
		t.Fatal(e.legacyPing)
	}
	if got := e.download(); got[:44] != "https://sp.example.ir:8080/download?size=250" {
		t.Fatal(got)
	}
}
