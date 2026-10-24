package devices

import "testing"

func TestVendor(t *testing.T) {
	for mac, want := range map[string]string{
		"f0:d1:a9:12:34:56": "Apple",
		"B8:27:EB:00:00:01": "Raspberry Pi Foundation",
		"3c:5a:b4:aa:bb:cc": "Google",
		"62:b0:4c:91:aa:5e": "", // random (locally administered)
	} {
		if got := Vendor(mac); got != want {
			t.Errorf("Vendor(%s) = %q, want %q", mac, got, want)
		}
	}
}
