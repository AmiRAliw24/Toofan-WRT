package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The list published in the repository (raw.githubusercontent.com) must
// always pass the router's own check, and match the built-in one.
func TestRepositoryListIsValid(t *testing.T) {
	b, err := os.ReadFile("../../../dns-providers.json")
	if err != nil {
		t.Fatal(err)
	}
	var f providerFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if err := validProviders(f.Providers); err != nil {
		t.Fatal(err)
	}
	if f.Updated == 0 {
		t.Fatal("updated date missing")
	}
	if err := validProviders(builtinProviders); err != nil {
		t.Fatal("built-in list:", err)
	}
	ids := map[string]bool{}
	for _, p := range f.Providers {
		ids[p.ID] = true
	}
	for _, p := range builtinProviders {
		if !ids[p.ID] {
			t.Fatalf("%s is built in but missing from the repository list (a saved choice would vanish)", p.ID)
		}
	}
}

func TestValidProvidersRefusesBadLists(t *testing.T) {
	good := append([]DNSProvider{}, builtinProviders...)
	for name, mut := range map[string]func([]DNSProvider) []DNSProvider{
		"hostname server": func(l []DNSProvider) []DNSProvider { l[1].Servers = []string{"dns.example"}; return l },
		"ipv6 server":     func(l []DNSProvider) []DNSProvider { l[1].Servers = []string{"2606:4700::1111"}; return l },
		"no isp":          func(l []DNSProvider) []DNSProvider { return l[1:] },
		"http doh":        func(l []DNSProvider) []DNSProvider { l[1].DoH = "http://x/dns-query"; return l },
		"dup id":          func(l []DNSProvider) []DNSProvider { l[2].ID = l[1].ID; return l },
		"bad id":          func(l []DNSProvider) []DNSProvider { l[1].ID = "Cloud Flare"; return l },
	} {
		l := make([]DNSProvider, len(good))
		copy(l, good)
		for i := range l {
			l[i].Servers = append([]string{}, good[i].Servers...)
		}
		if validProviders(mut(l)) == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestUpdateProviders(t *testing.T) {
	list := append([]DNSProvider{}, builtinProviders...)
	list = append(list, DNSProvider{ID: "newdns", Name: "New", NameFa: "جدید", Servers: []string{"5.6.7.8"}, Domestic: true})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.Write([]byte(`{"providers":[{"id":"x"}]}`))
			return
		}
		json.NewEncoder(w).Encode(providerFile{Updated: 1793500000, Providers: list})
	}))
	defer srv.Close()
	dir := t.TempDir()
	defer func(f, u string) { ProvidersFile, ProvidersURL = f, u; providers, provUpdated = builtinProviders, 0 }(ProvidersFile, ProvidersURL)
	ProvidersFile = filepath.Join(dir, "dns-providers.json")

	ProvidersURL = srv.URL + "/bad"
	if _, err := UpdateProviders(); err == nil || FindProvider("shecan") == nil {
		t.Fatal("a broken list must be refused and the current one kept")
	}
	ProvidersURL = srv.URL + "/ok"
	if n, err := UpdateProviders(); err != nil || n != len(list) || FindProvider("newdns") == nil || ProvidersUpdated() != 1793500000 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	// survives a restart
	providers, provUpdated = builtinProviders, 0
	LoadProviders()
	if FindProvider("newdns") == nil {
		t.Fatal("saved list not loaded")
	}
}
