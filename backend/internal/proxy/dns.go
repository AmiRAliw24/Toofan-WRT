package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"toofanwrt/internal/fetch"

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
	// Paid: works only with a subscription registered on the provider's site.
	Paid bool `json:"paid,omitempty"`
	// A short note (outages, requirements) shown on the provider's tile;
	// it comes with the updatable list, so it can follow the news.
	Note   string `json:"note,omitempty"`
	NoteFa string `json:"noteFa,omitempty"`
}

// builtinProviders ships with the panel; the list can be refreshed from the
// project repository (UpdateProviders) when a provider changes addresses.
var builtinProviders = []DNSProvider{
	{ID: "isp", Name: "Modem / ISP", NameFa: "DNS مودم", Domestic: true},
	{ID: "cloudflare", Name: "Cloudflare", NameFa: "کلادفلر", Servers: []string{"1.1.1.1", "1.0.0.1"}, DoH: "https://1.1.1.1/dns-query"},
	{ID: "cloudflare-security", Name: "Cloudflare (malware)", NameFa: "کلادفلر (ضدبدافزار)", Servers: []string{"1.1.1.2", "1.0.0.2"}, DoH: "https://security.cloudflare-dns.com/dns-query"},
	{ID: "cloudflare-family", Name: "Cloudflare (family)", NameFa: "کلادفلر (خانواده)", Servers: []string{"1.1.1.3", "1.0.0.3"}, DoH: "https://family.cloudflare-dns.com/dns-query"},
	{ID: "google", Name: "Google", NameFa: "گوگل", Servers: []string{"8.8.8.8", "8.8.4.4"}, DoH: "https://dns.google/dns-query"},
	{ID: "quad9", Name: "Quad9", NameFa: "کواد۹", Servers: []string{"9.9.9.9", "149.112.112.112"}, DoH: "https://dns.quad9.net/dns-query"},
	{ID: "adguard", Name: "AdGuard (ads)", NameFa: "ادگارد (ضدتبلیغ)", Servers: []string{"94.140.14.14", "94.140.15.15"}, DoH: "https://dns.adguard-dns.com/dns-query"},
	{ID: "shecan", Name: "Shecan (free)", NameFa: "شکن (رایگان)", Servers: []string{"178.22.122.100", "185.51.200.2"}, Domestic: true,
		Note: "Shecan reports its free service is disrupted on some ISPs.", NoteFa: "شکن اعلام کرده سرویس رایگانش روی بعضی اپراتورها اختلال دارد."},
	{ID: "shecan-pro", Name: "Shecan Pro", NameFa: "شکن حرفه‌ای", Servers: []string{"178.22.122.101", "185.51.200.1"}, Domestic: true, Paid: true,
		Note: "Needs a Shecan Pro subscription with this line's IP registered.", NoteFa: "اشتراک شکن حرفه‌ای و ثبت IP همین خط در پنل شکن لازم است."},
	{ID: "electro", Name: "Electro", NameFa: "الکترو", Servers: []string{"78.157.42.100", "78.157.42.101"}, Domestic: true},
	{ID: "begzar", Name: "Begzar", NameFa: "بگذر", Servers: []string{"185.55.226.26", "185.55.225.25"}, Domestic: true},
	{ID: "radar", Name: "Radar Game", NameFa: "رادار گیم", Servers: []string{"10.202.10.10", "10.202.10.11"}, Domestic: true},
	{ID: "403", Name: "403 Online", NameFa: "۴۰۳ آنلاین", Servers: []string{"10.202.10.202", "10.202.10.102"}, Domestic: true},
	{ID: "pishgaman", Name: "Pishgaman", NameFa: "پیشگامان", Servers: []string{"5.202.100.100", "5.202.100.101"}, Domestic: true},
	{ID: "shatel", Name: "Shatel", NameFa: "شاتل", Servers: []string{"85.15.1.14", "85.15.1.15"}, Domestic: true},
}

var (
	provMu      sync.RWMutex
	providers   = builtinProviders
	provUpdated int64 // unix time of the downloaded list, 0 = built in

	// ProvidersFile keeps the last downloaded list across restarts.
	ProvidersFile = "/etc/toofanwrt/dns-providers.json"
	ProvidersURL  = "https://raw.githubusercontent.com/AmiRAliw24/Toofan-WRT/main/dns-providers.json"
)

// Providers returns the provider list shown on the DNS page.
func Providers() []DNSProvider {
	provMu.RLock()
	defer provMu.RUnlock()
	return providers
}

// ProvidersUpdated is when the list was last downloaded (0 = built in).
func ProvidersUpdated() int64 {
	provMu.RLock()
	defer provMu.RUnlock()
	return provUpdated
}

func FindProvider(id string) *DNSProvider {
	provMu.RLock()
	defer provMu.RUnlock()
	for i := range providers {
		if providers[i].ID == id {
			p := providers[i]
			return &p
		}
	}
	return nil
}

type providerFile struct {
	Updated   int64         `json:"updated"`
	Providers []DNSProvider `json:"providers"`
}

var providerIDRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// validProviders refuses a list that would break DNS: every entry needs an
// id, a name and plain IPv4 servers (and an https DoH URL if any); "isp" and
// at least one domestic and one foreign provider must stay.
func validProviders(list []DNSProvider) error {
	if len(list) < 3 || len(list) > 60 {
		return errors.New("unexpected list size")
	}
	seen := map[string]bool{}
	domestic, foreign := false, false
	for _, p := range list {
		if !providerIDRe.MatchString(p.ID) || seen[p.ID] || p.Name == "" || p.NameFa == "" {
			return fmt.Errorf("bad provider %q", p.ID)
		}
		seen[p.ID] = true
		if p.ID != "isp" && (len(p.Servers) == 0 || len(p.Servers) > 4) {
			return fmt.Errorf("provider %q: 1-4 servers", p.ID)
		}
		for _, sv := range p.Servers {
			if ip := net.ParseIP(sv); ip == nil || ip.To4() == nil {
				return fmt.Errorf("provider %q: %q is not an IPv4 address", p.ID, sv)
			}
		}
		if p.DoH != "" && !strings.HasPrefix(p.DoH, "https://") {
			return fmt.Errorf("provider %q: DoH must be https", p.ID)
		}
		if len(p.Note) > 300 || len(p.NoteFa) > 300 {
			return fmt.Errorf("provider %q: note too long", p.ID)
		}
		if p.ID != "isp" {
			domestic = domestic || p.Domestic
			foreign = foreign || !p.Domestic
		}
	}
	if !seen["isp"] || !domestic || !foreign {
		return errors.New("the list must keep the ISP entry and both kinds of DNS")
	}
	return nil
}

// LoadProviders reads the last downloaded list (at start).
func LoadProviders() {
	b, err := os.ReadFile(ProvidersFile)
	if err != nil {
		return
	}
	var f providerFile
	if json.Unmarshal(b, &f) != nil || validProviders(f.Providers) != nil {
		return
	}
	provMu.Lock()
	providers, provUpdated = f.Providers, f.Updated
	provMu.Unlock()
}

// UpdateProviders downloads the current list from the project repository.
func UpdateProviders() (int, error) {
	var f providerFile
	code, err := fetch.JSON("GET", ProvidersURL, nil, &f, time.Minute)
	if err == nil && code != 200 {
		err = fmt.Errorf("HTTP %d", code)
	}
	if err != nil {
		return 0, fmt.Errorf("could not download the DNS list: %v", err)
	}
	if err := validProviders(f.Providers); err != nil {
		return 0, fmt.Errorf("the downloaded DNS list was refused: %v", err)
	}
	if f.Updated == 0 {
		f.Updated = time.Now().Unix()
	}
	b, _ := json.MarshalIndent(f, "", "  ")
	os.MkdirAll(filepath.Dir(ProvidersFile), 0o755)
	if err := os.WriteFile(ProvidersFile+".tmp", b, 0o644); err == nil {
		os.Rename(ProvidersFile+".tmp", ProvidersFile)
	}
	provMu.Lock()
	providers, provUpdated = f.Providers, f.Updated
	provMu.Unlock()
	return len(f.Providers), nil
}

// DomesticDNSFor returns the chosen Iranian DNS (Shecan, Electro, …), or
// nil when the choice is not a domestic provider.
func DomesticDNSFor(d config.DNS) []string {
	if p := FindProvider(d.Provider); p != nil && p.Domestic && len(p.Servers) > 0 {
		return p.Servers
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
