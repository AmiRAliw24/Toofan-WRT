// Package netinfo finds the public IP and ISP of the line (direct) and of
// the tunnel's exit (through Xray's SOCKS port).
package netinfo

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"toofanwrt/internal/env"
)

type Info struct {
	IP          string `json:"ip"`
	ISP         string `json:"isp"`
	ISPFa       string `json:"ispFa,omitempty"`
	Country     string `json:"country"`
	CountryCode string `json:"countryCode"`
	City        string `json:"city"`
	At          int64  `json:"at"`
	Error       string `json:"error,omitempty"`
}

func client(socksPort int) *http.Client {
	tr := &http.Transport{}
	if socksPort > 0 {
		pu, _ := url.Parse(fmt.Sprintf("socks5h://127.0.0.1:%d", socksPort))
		tr.Proxy = http.ProxyURL(pu)
	}
	return &http.Client{Transport: tr, Timeout: 8 * time.Second}
}

func getJSON(cl *http.Client, u string, v any) error {
	resp, err := cl.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(v)
}

// Lookup asks a few IP-info services in turn; socksPort > 0 goes through the tunnel.
func Lookup(socksPort int) Info {
	if env.Mock {
		if socksPort > 0 {
			return Info{IP: "185.220.101.44", ISP: "Hetzner Online GmbH", Country: "Germany", CountryCode: "DE", City: "Falkenstein", At: time.Now().Unix()}
		}
		return withFa(Info{IP: "5.120.33.71", ISP: "MTN Irancell", Country: "Iran", CountryCode: "IR", City: "Tehran", At: time.Now().Unix()})
	}
	cl := client(socksPort)
	var errs []string

	var a struct {
		Status      string `json:"status"`
		Message     string `json:"message"`
		Query       string `json:"query"`
		ISP         string `json:"isp"`
		Org         string `json:"org"`
		Country     string `json:"country"`
		CountryCode string `json:"countryCode"`
		City        string `json:"city"`
	}
	if err := getJSON(cl, "http://ip-api.com/json/?fields=status,message,query,isp,org,country,countryCode,city", &a); err == nil && a.Status == "success" {
		return withFa(Info{IP: a.Query, ISP: first(a.ISP, a.Org), Country: a.Country, CountryCode: a.CountryCode, City: a.City, At: time.Now().Unix()})
	} else if err != nil {
		errs = append(errs, err.Error())
	}

	var b struct {
		Success     bool   `json:"success"`
		IP          string `json:"ip"`
		Country     string `json:"country"`
		CountryCode string `json:"country_code"`
		City        string `json:"city"`
		Connection  struct {
			ISP string `json:"isp"`
			Org string `json:"org"`
		} `json:"connection"`
	}
	if err := getJSON(cl, "https://ipwho.is/", &b); err == nil && b.Success {
		return withFa(Info{IP: b.IP, ISP: first(b.Connection.ISP, b.Connection.Org), Country: b.Country, CountryCode: b.CountryCode, City: b.City, At: time.Now().Unix()})
	} else if err != nil {
		errs = append(errs, err.Error())
	}

	// Last resort: Cloudflare's trace gives at least the IP and country.
	if ip, cc, err := cfTrace(cl); err == nil {
		return Info{IP: ip, CountryCode: cc, At: time.Now().Unix()}
	} else {
		errs = append(errs, err.Error())
	}
	return Info{At: time.Now().Unix(), Error: strings.Join(errs, "; ")}
}

func cfTrace(cl *http.Client) (ip, cc string, err error) {
	resp, err := cl.Get("https://1.1.1.1/cdn-cgi/trace")
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	for _, line := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "ip":
			ip = v
		case "loc":
			cc = v
		}
	}
	if ip == "" {
		return "", "", errors.New("no ip in trace")
	}
	return ip, cc, nil
}

func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Persian names of the common Iranian operators, matched on the ISP string.
var faNames = []struct{ key, fa string }{
	{"irancell", "ایرانسل"}, {"iran cell", "ایرانسل"}, {"mtn", "ایرانسل"},
	{"mobile communication company of iran", "همراه اول"}, {"mci", "همراه اول"}, {"hamrah", "همراه اول"},
	{"rightel", "رایتل"},
	{"telecommunication company of iran", "مخابرات"}, {"iran telecommunication", "مخابرات"}, {"tci", "مخابرات"},
	{"shatel", "شاتل"}, {"aria shatel", "شاتل"},
	{"pars online", "پارس‌آنلاین"}, {"parsonline", "پارس‌آنلاین"},
	{"asiatech", "آسیاتک"}, {"asia tech", "آسیاتک"},
	{"mobinnet", "مبین‌نت"}, {"mobin net", "مبین‌نت"},
	{"afranet", "افرانت"}, {"respina", "رسپینا"}, {"hiweb", "های‌وب"}, {"hi web", "های‌وب"},
	{"pishgaman", "پیشگامان"}, {"fanava", "فن‌آوا"}, {"sabanet", "صبانت"}, {"zitel", "زیتل"},
	{"iran information technology", "زیرساخت"}, {"tic", "زیرساخت"}, {"pars abr", "پارس‌ابر"}, {"arvan", "آروان"},
}

func withFa(i Info) Info {
	l := strings.ToLower(i.ISP)
	for _, n := range faNames {
		if strings.Contains(l, n.key) {
			i.ISPFa = n.fa
			break
		}
	}
	return i
}
