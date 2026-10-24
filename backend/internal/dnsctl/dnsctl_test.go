package dnsctl

import (
	"strings"
	"testing"
)

// OpenWrt's generated dnsmasq config already sets these; repeating any of
// them in our drop-in makes dnsmasq refuse to start ("illegal repeated
// keyword"), which takes DNS down for the whole network.
var setByOpenWrt = []string{"cache-size", "log-async", "edns-packet-max", "domain=", "local=", "port=",
	"dhcp-leasefile", "resolv-file", "conf-dir", "addn-hosts", "dhcp-range", "log-facility"}

func TestRenderAvoidsRepeatedKeywords(t *testing.T) {
	for _, p := range []Plan{
		{TunnelPort: 5353},
		{TunnelPort: 5353, LogQueries: true},
		{Servers: []string{"1.1.1.1", "8.8.8.8"}, LogQueries: true},
		{},
	} {
		out := render(p)
		for _, line := range strings.Split(out, "\n") {
			for _, k := range setByOpenWrt {
				if strings.HasPrefix(line, k) {
					t.Errorf("drop-in sets %q, which OpenWrt already sets: %q", k, line)
				}
			}
		}
	}
}
