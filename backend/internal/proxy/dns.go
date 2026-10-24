package proxy

import (
	"strings"

	"toofanwrt/internal/config"
)

type DNSProvider struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	NameFa  string   `json:"nameFa"`
	Servers []string `json:"servers"`
	DoH     string   `json:"doh,omitempty"`
	// Domestic providers answer for filtered sites too (anti-sanction DNS).
	Domestic bool `json:"domestic"`
}

// Providers is the list shown on the DNS page.
var Providers = []DNSProvider{
	{ID: "isp", Name: "Modem / ISP", NameFa: "DNS مودم", Domestic: true},
	{ID: "cloudflare", Name: "Cloudflare", NameFa: "کلادفلر", Servers: []string{"1.1.1.1", "1.0.0.1"}, DoH: "https://1.1.1.1/dns-query"},
	{ID: "cloudflare-security", Name: "Cloudflare (malware)", NameFa: "کلادفلر (ضدبدافزار)", Servers: []string{"1.1.1.2", "1.0.0.2"}, DoH: "https://security.cloudflare-dns.com/dns-query"},
	{ID: "cloudflare-family", Name: "Cloudflare (family)", NameFa: "کلادفلر (خانواده)", Servers: []string{"1.1.1.3", "1.0.0.3"}, DoH: "https://family.cloudflare-dns.com/dns-query"},
	{ID: "google", Name: "Google", NameFa: "گوگل", Servers: []string{"8.8.8.8", "8.8.4.4"}, DoH: "https://dns.google/dns-query"},
	{ID: "quad9", Name: "Quad9", NameFa: "کواد۹", Servers: []string{"9.9.9.9", "149.112.112.112"}, DoH: "https://dns.quad9.net/dns-query"},
	{ID: "adguard", Name: "AdGuard (ads)", NameFa: "ادگارد (ضدتبلیغ)", Servers: []string{"94.140.14.14", "94.140.15.15"}, DoH: "https://dns.adguard-dns.com/dns-query"},
	{ID: "shecan", Name: "Shecan", NameFa: "شکن", Servers: []string{"178.22.122.100", "185.51.200.2"}, Domestic: true},
	{ID: "electro", Name: "Electro", NameFa: "الکترو", Servers: []string{"78.157.42.100", "78.157.42.101"}, Domestic: true},
	{ID: "begzar", Name: "Begzar", NameFa: "بگذر", Servers: []string{"185.55.226.26", "185.55.225.25"}, Domestic: true},
	{ID: "radar", Name: "Radar Game", NameFa: "رادار گیم", Servers: []string{"10.202.10.10", "10.202.10.11"}, Domestic: true},
	{ID: "403", Name: "403 Online", NameFa: "۴۰۳ آنلاین", Servers: []string{"10.202.10.202", "10.202.10.102"}, Domestic: true},
}

func FindProvider(id string) *DNSProvider {
	for i := range Providers {
		if Providers[i].ID == id {
			return &Providers[i]
		}
	}
	return nil
}

// SecureDNSFor picks the resolvers Xray queries through the tunnel.
// Domestic providers make no sense inside the tunnel, so they fall back to Cloudflare.
func SecureDNSFor(d config.DNS) []string {
	if d.Provider == "custom" {
		if list := strings.Fields(strings.ReplaceAll(d.Custom, ",", " ")); len(list) > 0 {
			return list
		}
	}
	if p := FindProvider(d.Provider); p != nil && !p.Domestic && p.DoH != "" {
		return []string{p.DoH}
	}
	return []string{"https://1.1.1.1/dns-query"}
}
