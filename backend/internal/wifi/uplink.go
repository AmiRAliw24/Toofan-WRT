package wifi

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"toofanwrt/internal/env"
	"toofanwrt/internal/uci"
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

// A second Wi-Fi uplink (on the other radio) adds bandwidth when speed
// aggregation is on. Each radio can hold one uplink: it has to follow its
// modem's channel.
type slotInfo struct {
	Net, Section, Ifname, Metric string
}

var slots = map[int]slotInfo{
	1: {UplinkNet, uplinkSection, uplinkIfname, "20"},
	2: {"wwan2", "separ_uplink2", "wl-uplink2", "30"},
}

// Slots lists the uplink slot numbers.
func Slots() []int { return []int{1, 2} }

// SlotNet is the netifd interface of an uplink slot.
func SlotNet(slot int) string { return slots[slot].Net }

func slotOf(slot int) (slotInfo, error) {
	si, ok := slots[slot]
	if !ok {
		return slotInfo{}, errors.New("invalid uplink slot")
	}
	return si, nil
}

type Network struct {
	SSID       string `json:"ssid"`
	BSSID      string `json:"bssid"`
	Band       string `json:"band"`
	Channel    int    `json:"channel"`
	Signal     int    `json:"signal"`     // dBm
	Encryption string `json:"encryption"` // uci value; "enterprise" = unsupported
}

type Uplink struct {
	Slot       int    `json:"slot"`
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
	mockUp   = map[int]Uplink{}
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
	st, err := Get()
	if err != nil {
		return nil, err
	}
	var out []Network
	var lastErr error
	scanned := 0
	for _, b := range st.Bands {
		// iwinfo accepts the radio name and spawns a temporary interface when
		// the radio has none (Wi-Fi off), the same way LuCI's Scan works.
		devs := []string{b.Radio}
		if dev, _, err := apDevice(b.Band); err == nil {
			devs = append([]string{dev}, devs...)
		}
		var raw string
		for _, dev := range devs {
			raw, err = env.Run(35*time.Second, "ubus", "-t", "30", "call", "iwinfo", "scan", `{"device":"`+dev+`"}`)
			if err == nil {
				break
			}
		}
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
			out = append(out, Network{SSID: r.SSID, BSSID: strings.ToLower(r.BSSID), Band: b.Band,
				Channel: r.Channel, Signal: r.Signal, Encryption: encFromScan(r.Encryption)})
		}
	}
	if scanned == 0 {
		if lastErr == nil {
			lastErr = errors.New("no Wi-Fi radio found")
		}
		return nil, fmt.Errorf("scan failed: %v", lastErr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Signal > out[j].Signal })
	return out, nil
}

func UplinkStatus() Uplink { return UplinkStatusSlot(1) }

func UplinkStatusSlot(slot int) Uplink {
	u := Uplink{Slot: slot}
	si, err := slotOf(slot)
	if err != nil {
		return u
	}
	if env.Mock {
		mockUpMu.Lock()
		defer mockUpMu.Unlock()
		if m, ok := mockUp[slot]; ok {
			return m
		}
		return u
	}
	m, err := uci.Show("wireless")
	if err != nil {
		return u
	}
	p := "wireless." + si.Section + "."
	if m["wireless."+si.Section] != "wifi-iface" || m[p+"disabled"] == "1" {
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
	if info, err := env.Ubus("iwinfo", "info", `{"device":"`+si.Ifname+`"}`); err == nil {
		var i struct {
			SSID   string `json:"ssid"`
			BSSID  string `json:"bssid"`
			Signal int    `json:"signal"`
		}
		json.Unmarshal([]byte(info), &i)
		u.Connected = i.BSSID != "" && i.BSSID != "00:00:00:00:00:00" && i.Signal != 0
		u.Signal = i.Signal
	}
	if st, err := env.Ubus("network.interface."+si.Net, "status", ""); err == nil {
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
func ConnectUplink(n Network, key string) error { return ConnectUplinkSlot(1, n, key) }

// ConnectUplinkSlot joins a network on one uplink slot. The two slots must
// use different radios (bands).
func ConnectUplinkSlot(slot int, n Network, key string) error {
	si, err := slotOf(slot)
	if err != nil {
		return err
	}
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
	for _, other := range Slots() {
		if other == slot {
			continue
		}
		if o := UplinkStatusSlot(other); o.Configured && o.Band == n.Band {
			return fmt.Errorf("the %s radio already holds the other Wi-Fi uplink (%s); pick a network on the other band", bandName(n.Band), o.SSID)
		}
	}
	if env.Mock {
		mockUpMu.Lock()
		mockUp[slot] = Uplink{Slot: slot, Configured: true, SSID: n.SSID, Band: n.Band, Connected: true, Signal: n.Signal, IP: fmt.Sprintf("192.168.%d.150", 69+slot)}
		mockUpMu.Unlock()
		return nil
	}
	st, err := Get()
	if err != nil {
		return err
	}
	var band *Band
	for i := range st.Bands {
		if st.Bands[i].Band == n.Band {
			band = &st.Bands[i]
		}
	}
	if band == nil {
		return errors.New("no radio for this band")
	}
	radio := band.Radio
	b := uci.NewBatch()
	// Turning the radio on for the uplink must not also publish the
	// passwordless default "OpenWrt" network of a fresh router.
	if !band.Enabled && band.Section != "" && band.Encryption == "none" {
		b.Set("wireless."+band.Section+".disabled", "1")
	}
	b.Set("network."+si.Net, "interface")
	b.Set("network."+si.Net+".proto", "dhcp")
	b.Set("network."+si.Net+".metric", si.Metric) // the cable (metric 0) wins when present
	sec := "wireless." + si.Section
	b.Set(sec, "wifi-iface")
	b.Set(sec+".device", radio)
	b.Set(sec+".mode", "sta")
	b.Set(sec+".network", si.Net)
	b.Set(sec+".ifname", si.Ifname)
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
	if zone := wanZone(); zone != "" && !zoneHas(zone, si.Net) {
		b.AddList(zone+".network", si.Net)
	}
	if err := b.Commit(); err != nil {
		return err
	}
	env.Run(30*time.Second, "/etc/init.d/network", "reload")
	env.Run(30*time.Second, "/etc/init.d/firewall", "reload")
	_, err = env.Run(30*time.Second, "wifi", "reload")
	return err
}

func DisconnectUplink() error { return DisconnectUplinkSlot(1) }

func DisconnectUplinkSlot(slot int) error {
	si, err := slotOf(slot)
	if err != nil {
		return err
	}
	if env.Mock {
		mockUpMu.Lock()
		delete(mockUp, slot)
		mockUpMu.Unlock()
		return nil
	}
	if err := uci.NewBatch().Delete("wireless." + si.Section).Commit(); err != nil {
		return err
	}
	_, err = env.Run(30*time.Second, "wifi", "reload")
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

func bandName(b string) string {
	if b == "5g" {
		return "5 GHz"
	}
	return "2.4 GHz"
}

// RadioUplink returns the SSID of the uplink a radio holds ("" when none).
// Such a radio has to stay on the modem's channel.
func RadioUplink(radio string) string {
	if env.Mock {
		return ""
	}
	m, err := uci.Show("wireless")
	if err != nil {
		return ""
	}
	for _, si := range slots {
		p := "wireless." + si.Section + "."
		if m["wireless."+si.Section] == "wifi-iface" && m[p+"disabled"] != "1" && m[p+"device"] == radio {
			return m[p+"ssid"]
		}
	}
	return ""
}
