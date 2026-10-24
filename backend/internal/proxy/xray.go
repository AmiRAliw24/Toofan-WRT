package proxy

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"separ/internal/config"
)

const (
	TProxyPort = 12345
	SocksPort  = 10808
	DNSPort    = 5353
	// Mark set on Xray's own sockets so they never get re-captured.
	OutMark = 255
)

const assetIran = "ext:iran.dat:ir"

type BuildOpts struct {
	Server    config.Server
	SplitIran bool
	BlockAds  bool
	ISPDNS    []string // resolvers of the upstream line, used for Iranian names
	SecureDNS string   // resolver used through the tunnel
	HaveIran  bool     // iran.dat is installed
}

// BuildConfig renders the full Xray config for the transparent proxy.
func BuildConfig(o BuildOpts) ([]byte, error) {
	ob, _, err := Outbound(o.Server.Link)
	if err != nil {
		return nil, err
	}
	ob.Tag = "proxy"
	if ob.StreamSettings == nil {
		ob.StreamSettings = &StreamSettings{}
	}
	ob.StreamSettings.Sockopt = map[string]any{"mark": OutMark, "domainStrategy": "UseIPv4"}

	isp := ispResolvers(o.ISPDNS)
	direct := []string{}
	if net.ParseIP(o.Server.Address) == nil {
		// The proxy's own hostname must be resolved outside the tunnel.
		direct = append(direct, "full:"+o.Server.Address)
	}
	if o.SplitIran {
		direct = append(direct, "domain:ir")
		if o.HaveIran {
			direct = append(direct, assetIran)
		}
	}

	var dnsServers []any
	for _, ip := range isp {
		if len(direct) > 0 {
			dnsServers = append(dnsServers, map[string]any{
				"address": ip, "port": 53, "domains": direct, "skipFallback": true,
			})
		}
	}
	secure := o.SecureDNS
	if secure == "" {
		secure = "https://1.1.1.1/dns-query"
	}
	dnsServers = append(dnsServers, secure)
	if !strings.HasPrefix(secure, "https://8.8.8.8") {
		dnsServers = append(dnsServers, "https://8.8.8.8/dns-query")
	}

	var rules []any
	rules = append(rules,
		map[string]any{"type": "field", "inboundTag": []string{"dns-in"}, "outboundTag": "dns-out"},
	)
	if len(isp) > 0 {
		rules = append(rules, map[string]any{"type": "field", "ip": isp, "port": "53", "outboundTag": "direct"})
	}
	rules = append(rules,
		map[string]any{"type": "field", "inboundTag": []string{"dns-internal"}, "outboundTag": "proxy"},
		map[string]any{"type": "field", "ip": []string{"geoip:private"}, "outboundTag": "direct"},
	)
	if o.BlockAds && o.HaveIran {
		rules = append(rules, map[string]any{"type": "field", "domain": []string{"ext:iran.dat:ads"}, "outboundTag": "block"})
	}
	if o.SplitIran {
		dom := []string{"domain:ir"}
		if o.HaveIran {
			dom = append(dom, assetIran)
		}
		rules = append(rules,
			map[string]any{"type": "field", "domain": dom, "outboundTag": "direct"},
			map[string]any{"type": "field", "ip": []string{"geoip:ir"}, "outboundTag": "direct"},
		)
	}
	rules = append(rules, map[string]any{"type": "field", "network": "tcp,udp", "outboundTag": "proxy"})

	sockDirect := map[string]any{"mark": OutMark}
	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"dns": map[string]any{
			"tag":           "dns-internal",
			"queryStrategy": "UseIPv4",
			"servers":       dnsServers,
		},
		"inbounds": []any{
			map[string]any{
				"tag": "tproxy", "port": TProxyPort, "protocol": "dokodemo-door",
				"settings":       map[string]any{"network": "tcp,udp", "followRedirect": true},
				"streamSettings": map[string]any{"sockopt": map[string]any{"tproxy": "tproxy"}},
				"sniffing": map[string]any{
					"enabled": true, "routeOnly": true,
					"destOverride": []string{"http", "tls", "quic"},
				},
			},
			map[string]any{
				"tag": "socks", "listen": "127.0.0.1", "port": SocksPort, "protocol": "socks",
				"settings": map[string]any{"udp": true},
			},
			map[string]any{
				"tag": "dns-in", "listen": "127.0.0.1", "port": DNSPort, "protocol": "dokodemo-door",
				"settings": map[string]any{"address": "1.1.1.1", "port": 53, "network": "tcp,udp"},
			},
		},
		"outbounds": []any{
			ob,
			map[string]any{"tag": "direct", "protocol": "freedom",
				"settings":       map[string]any{"domainStrategy": "UseIPv4"},
				"streamSettings": map[string]any{"sockopt": sockDirect}},
			map[string]any{"tag": "block", "protocol": "blackhole"},
			map[string]any{"tag": "dns-out", "protocol": "dns",
				"streamSettings": map[string]any{"sockopt": sockDirect}},
		},
		"routing": map[string]any{"domainStrategy": "AsIs", "rules": rules},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// BuildTestConfig makes one Xray instance that exposes a local SOCKS port
// per server, so all servers can be measured in parallel.
func BuildTestConfig(servers []config.Server, basePort int, isp []string) ([]byte, []int, error) {
	inbounds := []any{}
	outbounds := []any{map[string]any{"tag": "direct", "protocol": "freedom"}}
	rules := []any{}
	ports := make([]int, len(servers))
	var hostnames []string
	for i, s := range servers {
		ob, _, err := Outbound(s.Link)
		if err != nil {
			ports[i] = -1
			continue
		}
		in, out := fmt.Sprintf("in%d", i), fmt.Sprintf("out%d", i)
		ob.Tag = out
		if ob.StreamSettings == nil {
			ob.StreamSettings = &StreamSettings{}
		}
		ob.StreamSettings.Sockopt = map[string]any{"mark": OutMark, "domainStrategy": "UseIPv4"}
		ports[i] = basePort + i
		inbounds = append(inbounds, map[string]any{
			"tag": in, "listen": "127.0.0.1", "port": ports[i], "protocol": "socks",
		})
		outbounds = append(outbounds, ob)
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{in}, "outboundTag": out})
		if net.ParseIP(s.Address) == nil {
			hostnames = append(hostnames, s.Address)
		}
	}
	dnsServers := []any{}
	for _, ip := range ispResolvers(isp) {
		dnsServers = append(dnsServers, ip)
	}
	dnsServers = append(dnsServers, "localhost")
	cfg := map[string]any{
		"log":       map[string]any{"loglevel": "error"},
		"dns":       map[string]any{"servers": dnsServers, "queryStrategy": "UseIPv4"},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"routing":   map[string]any{"rules": rules},
	}
	b, err := json.Marshal(cfg)
	return b, ports, err
}

func ispResolvers(in []string) []string {
	var out []string
	for _, s := range in {
		if ip := net.ParseIP(s); ip != nil && ip.To4() != nil && !ip.IsLoopback() {
			out = append(out, s)
		}
	}
	return out
}
