package core

import (
	"math/rand"
	"net"
	"sync"
	"time"

	"toofanwrt/internal/env"
	"toofanwrt/internal/proxy"
)

// DNSResult is one provider's live check from the router itself.
type DNSResult struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Ms     int    `json:"ms"`
	Server string `json:"server,omitempty"`
	// Bypass (domestic providers only): a sanctioned site resolved to the
	// provider's Iranian relay, i.e. the anti-sanction part works.
	Bypass *bool  `json:"bypass,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Names for the check: an Iranian site any DNS must answer, and a site that
// blocks Iran (Google's developer pages) that an anti-sanction DNS sends to
// its relay inside Iran.
const (
	dnsTestPlain     = "www.aparat.com"
	dnsTestSanctions = "developer.android.com"
)

// TestDNS queries every provider in parallel (plain DNS on port 53, the
// way the router uses them).
func (c *Core) TestDNS() []DNSResult {
	list := proxy.Providers()
	out := make([]DNSResult, len(list))
	if env.Mock {
		for i, p := range list {
			r := DNSResult{ID: p.ID, OK: p.ID != "shecan", Ms: 8 + rand.Intn(60)}
			if !r.OK {
				r.Error = "timeout"
			} else if p.Domestic && p.ID != "isp" {
				b := p.ID != "pishgaman"
				r.Bypass = &b
			}
			out[i] = r
		}
		time.Sleep(1200 * time.Millisecond)
		return out
	}
	iran := c.iran()
	var wg sync.WaitGroup
	for i, p := range list {
		servers := p.Servers
		if p.ID == "isp" {
			servers = c.ispDNS()
		}
		wg.Add(1)
		go func(i int, p proxy.DNSProvider, servers []string) {
			defer wg.Done()
			r := DNSResult{ID: p.ID}
			if len(servers) == 0 {
				r.Error = "no server"
				out[i] = r
				return
			}
			for _, sv := range servers {
				start := time.Now()
				_, err := resolveVia(net.JoinHostPort(sv, "53"), dnsTestPlain)
				if err == nil {
					r.OK, r.Ms, r.Server, r.Error = true, int(time.Since(start).Milliseconds()), sv, ""
					break
				}
				r.Error = err.Error()
			}
			if r.OK && p.Domestic && p.ID != "isp" && len(iran) > 0 {
				ips, err := resolveVia(net.JoinHostPort(r.Server, "53"), dnsTestSanctions)
				b := err == nil && len(ips) > 0 && inCIDRs(ips[0], iran)
				r.Bypass = &b
			}
			out[i] = r
		}(i, p, servers)
	}
	wg.Wait()
	return out
}

// providersLoop refreshes the DNS list from the project repository once a
// day (a provider that changes addresses is fixed without a panel update).
func (c *Core) providersLoop() {
	time.Sleep(3 * time.Minute)
	for {
		if _, err := proxy.UpdateProviders(); err != nil {
			env.Logf("dns list: %v", err)
		}
		time.Sleep(24 * time.Hour)
	}
}
