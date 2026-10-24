package wifi

import (
	"encoding/json"
	"testing"
)

func TestEncFromScan(t *testing.T) {
	cases := map[string]string{
		`{"enabled":false}`: "none",
		`{"enabled":true,"wpa":[2],"authentication":["psk"],"ciphers":["ccmp"]}`: "psk2",
		`{"enabled":true,"wpa":[1,2],"authentication":["psk"]}`:                  "psk-mixed",
		`{"enabled":true,"wpa":[2,3],"authentication":["psk","sae"]}`:            "sae-mixed",
		`{"enabled":true,"wpa":[3],"authentication":["sae"]}`:                    "sae",
		`{"enabled":true,"wpa":[2],"authentication":["802.1x"]}`:                 "enterprise",
	}
	for in, want := range cases {
		var m map[string]any
		json.Unmarshal([]byte(in), &m)
		if got := encFromScan(m); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
}
