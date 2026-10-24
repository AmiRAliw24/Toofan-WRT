package wifi

import (
	"encoding/json"
	"errors"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"separ/internal/env"
	"separ/internal/uci"
)

// The router can take its internet from another Wi-Fi network (the modem)
// instead of the WAN cable. The uplink is a client (STA) interface on one
// radio, attached to the "wwan" network that sits in the firewall's wan zone.
// The WAN cable keeps working and wins when both are connected.

const (
	UplinkNet     = "wwan"
	uplinkSection = "separ_uplink"
	uplinkIfname  = "wl-uplink"
)

type Network struct {
	SSID       string `json:"ssid"`
	BSSID      string `json:"bssid"`
	Band       string `json:"band"`
	Channel    int    `json:"channel"`
	Signal     int    `json:"signal"`     // dBm
	Encryption string `json:"encryption"` // uci value; "enterprise" = unsupported
}

type Uplink struct {
	Configured bool   `json:"configured"`
	SSID       string `json:"ssid"`
	Band       string `json:"band"`
	Connected  bool   `json:"connected"`
	Signal     int    `json:"signal"`
	IP         string `json:"ip"`
	Error      string `json:"error,omitempty"`
}

var (
	mockUpMu sync.Mutex
	mockUp   Uplink
)

func encFromScan(e map[string]any) string {
	if en, _ := e["enabled"].(bool); !en {
		return "none"
	}
	has := func(key, val string) bool {
		arr, _ := e[key].([]any)
		for _, x := range arr {
			if s, ok := x.(string); ok && s == val {
				return true
			}
		}
		return false
	}
	wpa := map[int]bool{}
	if arr, ok := e["wpa"].([]any); ok {
		for _, x := range arr {
			if f, ok := x.(float64); ok {
				wpa[int(f)] = true
			}
		}
	}
	switch {
	case has("authentication", "802.1x"):
		return "enterprise"
	case has("authentication", "sae") && has("authentication", "psk"):
		return "sae-mixed"
	case has("authentication", "sae"):
		return "sae"
	case wpa[1] && wpa[2]:
		return "psk-mixed"
	case wpa[1] && !wpa[2]:
		return "psk"
	}
	return "psk2"
}

// ScanNetworks lists nearby networks on every band that has an access point.
func ScanNetworks() ([]Network, error) {
	if env.Mock {
		names := []string{"Irancell-TD-5G", "MobinNet-Home", "TP-Link_8F2A", "Asiatech-ADSL", "Neighbor-WiFi"}
		var out []Network
		for i, n := range names {
			band := "2g"
			ch := []int{1, 6, 11}[i%3]
			if i%2 == 0 {
				band, ch = "5g", []int{36, 44, 149}[i%3]
			}
			out = append(out, Network{SSID: n, BSSID: "aa:bb:cc:00:00:0" + string(rune('1'+i)), Band: band, Channel: ch, Signal: -45 - rand.Intn(40), Encryption: []string{"psk2", "sae-mixed", "psk2", "none", "psk2"}[i]})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Signal > out[j].Signal })
		return out, nil
	}
	var out []Network
	var lastErr error
	scanned := 0
	for _, band := range []string{"2g", "5g"} {
		dev, _, err := apDevice(band)
		if err != nil {
			lastErr = err
			continue
		}
		raw, err := env.Run(30*time.Second, "ubus", "-t", "25", "call", "iwinfo", "scan", `{"device":"`+dev+`"}`)
		if err != nil {
			lastErr = err
			continue
		}
		scanned++
		var sr struct {
			Results []struct {
				SSID       string         `json:"ssid"`
				BSSID      string         `json:"bssid"`
				Channel    int            `json:"channel"`
				Signal     int            `json:"signal"`
				Encryption map[string]any `json:"encryption"`
			} `json:"results"`
		}
		json.Unmarshal([]byte(raw), &sr)
		for _, r := range sr.Results {
			if r.SSID == "" {
				continue // hidden networks
			}
			out = append(out, Network{SSID: r.SSID, BSSID: strings.ToLower(r.BSSID), Band: band,
				Channel: r.Channel, Signal: r.Signal, Encryption: encFromScan(r.Encryption)})
		}
	}
	if scanned == 0 {
		if lastErr == nil {
			lastErr = errors.New("no radio available")
		}
		return nil, errors.New("Wi-Fi must be enabled on at least one band to scan: " + lastErr.Error())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Signal > out[j].Signal })
	return out, nil
}

func UplinkStatus() Uplink {
	if env.Mock {
		mockUpMu.Lock()
		defer mockUpMu.Unlock()
		return mockUp
	}
	u := Uplink{}
	m, err := uci.Show("wireless")
	if err != nil {
		return u
	}
	p := "wireless." + uplinkSection + "."
	if m["wireless."+uplinkSection] != "wifi-iface" || m[p+"disabled"] == "1" {
		return u
	}
	u.Configured, u.SSID = true, m[p+"ssid"]
	if st, err := Get(); err == nil {
		for _, b := range st.Bands {
			if b.Radio == m[p+"device"] {
				u.Band = b.Band
			}
		}
	}
	if info, err := env.Ubus("iwinfo", "info", `{"device":"`+uplinkIfname+`"}`); err == nil {
		var i struct {
			SSID   string `json:"ssid"`
			BSSID  string `json:"bssid"`
			Signal int    `json:"signal"`
		}
		json.Unmarshal([]byte(info), &i)
		u.Connected = i.BSSID != "" && i.BSSID != "00:00:00:00:00:00" && i.Signal != 0
		u.Signal = i.Signal
	}
	if st, err := env.Ubus("network.interface."+UplinkNet, "status", ""); err == nil {
		var s struct {
			Up   bool `json:"up"`
			IPv4 []struct {
				Address string `json:"address"`
			} `json:"ipv4-address"`
		}
		json.Unmarshal([]byte(st), &s)
		if s.Up && len(s.IPv4) > 0 {
			u.IP = s.IPv4[0].Address
		}
	}
	return u
}

// ConnectUplink joins the given network as the router's internet source.
func ConnectUplink(n Network, key string) error {
	if n.SSID == "" || len(n.SSID) > 32 {
		return errors.New("invalid network name")
	}
	switch n.Encryption {
	case "none":
	case "psk", "psk2", "psk-mixed", "sae", "sae-mixed":
		if len(key) < 8 || len(key) > 63 {
			return errors.New("password must be 8-63 characters")
		}
	case "enterprise":
		return errors.New("enterprise (802.1x) networks are not supported")
	default:
		return errors.New("unknown security type")
	}
	if env.Mock {
		mockUpMu.Lock()
		mockUp = Uplink{Configured: true, SSID: n.SSID, Band: n.Band, Connected: true, Signal: n.Signal, IP: "192.168.70.150"}
		mockUpMu.Unlock()
		return nil
	}
	st, err := Get()
	if err != nil {
		return err
	}
	radio := ""
	for _, b := range st.Bands {
		if b.Band == n.Band {
			radio = b.Radio
		}
	}
	if radio == "" {
		return errors.New("no radio for this band")
	}
	b := uci.NewBatch()
	b.Set("network."+UplinkNet, "interface")
	b.Set("network."+UplinkNet+".proto", "dhcp")
	b.Set("network."+UplinkNet+".metric", "20") // the cable (metric 0) wins when present
	sec := "wireless." + uplinkSection
	b.Set(sec, "wifi-iface")
	b.Set(sec+".device", radio)
	b.Set(sec+".mode", "sta")
	b.Set(sec+".network", UplinkNet)
	b.Set(sec+".ifname", uplinkIfname)
	b.Set(sec+".ssid", n.SSID)
	b.Set(sec+".encryption", n.Encryption)
	b.Set(sec+".disabled", "0")
	b.Delete(sec + ".bssid") // roam between the modem's access points freely
	if n.Encryption == "none" {
		b.Delete(sec + ".key")
	} else {
		b.Set(sec+".key", key)
	}
	// A radio can serve only one channel: let it follow the modem's.
	b.Set("wireless."+radio+".channel", "auto")
	b.Set("wireless."+radio+".disabled", "0")
	if zone := wanZone(); zone != "" && !zoneHas(zone, UplinkNet) {
		b.AddList(zone+".network", UplinkNet)
	}
	if err := b.Commit(); err != nil {
		return err
	}
	env.Run(30*time.Second, "/etc/init.d/network", "reload")
	env.Run(30*time.Second, "/etc/init.d/firewall", "reload")
	_, err = env.Run(30*time.Second, "wifi", "reload")
	return err
}

func DisconnectUplink() error {
	if env.Mock {
		mockUpMu.Lock()
		mockUp = Uplink{}
		mockUpMu.Unlock()
		return nil
	}
	if err := uci.NewBatch().Delete("wireless." + uplinkSection).Commit(); err != nil {
		return err
	}
	_, err := env.Run(30*time.Second, "wifi", "reload")
	return err
}

// wanZone returns the uci path of the firewall zone named "wan".
func wanZone() string {
	m, err := uci.Show("firewall")
	if err != nil {
		return ""
	}
	for k, v := range m {
		if strings.HasSuffix(k, ".name") && v == "wan" {
			return strings.TrimSuffix(k, ".name")
		}
	}
	return ""
}

func zoneHas(zone, network string) bool {
	m, _ := uci.Show("firewall")
	for _, n := range strings.Fields(strings.ReplaceAll(m[zone+".network"], "'", "")) {
		if n == network {
			return true
		}
	}
	return false
}
