// Package wifi reads and writes the wireless settings through UCI.
package wifi

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"separ/internal/env"
	"separ/internal/uci"
)

type Band struct {
	Band       string `json:"band"` // 2g | 5g
	Radio      string `json:"radio"`
	Section    string `json:"section"`
	Enabled    bool   `json:"enabled"`
	SSID       string `json:"ssid"`
	Key        string `json:"key"`
	Encryption string `json:"encryption"`
	Channel    string `json:"channel"` // "auto" or a number
	HTMode     string `json:"htmode"`
	TxPower    int    `json:"txpower"` // dBm, 0 = default
	Hidden     bool   `json:"hidden"`
	Channels   []int  `json:"channels"`
}

type Guest struct {
	Enabled    bool   `json:"enabled"`
	SSID       string `json:"ssid"`
	Key        string `json:"key"`
	Encryption string `json:"encryption"`
	Exists     bool   `json:"exists"`
}

type State struct {
	Bands   []Band `json:"bands"`
	Guest   Guest  `json:"guest"`
	Country string `json:"country"`
}

const GuestNet = "guest"

var GuestIfnames = []string{"wl-guest0", "wl-guest1"}

var (
	mockMu    sync.Mutex
	mockState = State{
		Country: "IR",
		Bands: []Band{
			{Band: "2g", Radio: "radio0", Section: "default_radio0", Enabled: true, SSID: "Separ-Home", Key: "supersecret123", Encryption: "sae-mixed", Channel: "1", HTMode: "HT20", Channels: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}},
			{Band: "5g", Radio: "radio1", Section: "default_radio1", Enabled: true, SSID: "Separ-Home-5G", Key: "supersecret123", Encryption: "sae-mixed", Channel: "36", HTMode: "VHT80", Channels: []int{36, 40, 44, 48, 149, 153, 157, 161}},
		},
		Guest: Guest{SSID: "Separ-Guest", Encryption: "psk2", Key: "welcome2024"},
	}
)

// Get reads the current wireless configuration.
func Get() (State, error) {
	if env.Mock {
		mockMu.Lock()
		defer mockMu.Unlock()
		return mockState, nil
	}
	m, err := uci.Show("wireless")
	if err != nil {
		return State{}, err
	}
	st := State{}
	radios := map[string]*Band{}
	var radioNames []string
	for k, v := range m {
		parts := strings.Split(k, ".")
		if len(parts) == 2 && v == "wifi-device" {
			radioNames = append(radioNames, parts[1])
		}
	}
	sort.Strings(radioNames)
	for _, r := range radioNames {
		b := &Band{Radio: r, Channel: m["wireless."+r+".channel"], HTMode: m["wireless."+r+".htmode"]}
		b.Band = m["wireless."+r+".band"]
		if b.Band == "" { // pre-21.02 style
			if strings.Contains(m["wireless."+r+".hwmode"], "a") {
				b.Band = "5g"
			} else {
				b.Band = "2g"
			}
		}
		if b.Channel == "" {
			b.Channel = "auto"
		}
		b.TxPower, _ = strconv.Atoi(m["wireless."+r+".txpower"])
		if st.Country == "" {
			st.Country = m["wireless."+r+".country"]
		}
		b.Enabled = m["wireless."+r+".disabled"] != "1"
		radios[r] = b
	}
	// Main AP per radio = first ap iface on the lan network.
	var ifaceNames []string
	for k, v := range m {
		parts := strings.Split(k, ".")
		if len(parts) == 2 && v == "wifi-iface" {
			ifaceNames = append(ifaceNames, parts[1])
		}
	}
	sort.Strings(ifaceNames)
	for _, s := range ifaceNames {
		p := "wireless." + s + "."
		if m[p+"mode"] != "ap" {
			continue
		}
		if m[p+"network"] == GuestNet {
			st.Guest.Exists = true
			st.Guest.Enabled = st.Guest.Enabled || m[p+"disabled"] != "1"
			st.Guest.SSID, st.Guest.Key, st.Guest.Encryption = m[p+"ssid"], m[p+"key"], m[p+"encryption"]
			continue
		}
		b := radios[m[p+"device"]]
		if b == nil || b.Section != "" {
			continue
		}
		b.Section = s
		b.SSID, b.Key, b.Encryption = m[p+"ssid"], m[p+"key"], m[p+"encryption"]
		b.Hidden = m[p+"hidden"] == "1"
		if m[p+"disabled"] == "1" {
			b.Enabled = false
		}
	}
	for _, r := range radioNames {
		b := radios[r]
		b.Channels = channels(r)
		st.Bands = append(st.Bands, *b)
	}
	sort.Slice(st.Bands, func(i, j int) bool { return st.Bands[i].Band < st.Bands[j].Band })
	return st, nil
}

// channels lists the frequencies the radio allows in the current country.
func channels(radio string) []int {
	devs, err := env.Ubus("iwinfo", "devices", "")
	if err != nil {
		return nil
	}
	var d struct {
		Devices []string `json:"devices"`
	}
	json.Unmarshal([]byte(devs), &d)
	// The first interface whose frequency list matches the radio's band wins.
	for _, dev := range d.Devices {
		fl, err := env.Ubus("iwinfo", "freqlist", `{"device":"`+dev+`"}`)
		if err != nil {
			continue
		}
		var f struct {
			Results []struct {
				Channel    int  `json:"channel"`
				MHz        int  `json:"mhz"`
				Restricted bool `json:"restricted"`
			} `json:"results"`
		}
		json.Unmarshal([]byte(fl), &f)
		var out []int
		want5 := radioIs5G(radio)
		for _, r := range f.Results {
			if r.Restricted || (r.MHz > 4000) != want5 {
				continue
			}
			out = append(out, r.Channel)
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func radioIs5G(radio string) bool {
	out, _ := env.Run(3*time.Second, "uci", "-q", "get", "wireless."+radio+".band")
	if out != "" {
		return out == "5g"
	}
	out, _ = env.Run(3*time.Second, "uci", "-q", "get", "wireless."+radio+".hwmode")
	return strings.Contains(out, "a")
}

var validEnc = map[string]bool{"sae-mixed": true, "psk2": true, "sae": true, "psk-mixed": true, "none": true}

func validate(ssid, key, enc string) error {
	if ssid == "" || len(ssid) > 32 {
		return errors.New("ssid must be 1-32 bytes")
	}
	if !validEnc[enc] {
		return errors.New("invalid encryption")
	}
	if enc != "none" && (len(key) < 8 || len(key) > 63) {
		return errors.New("password must be 8-63 characters")
	}
	return nil
}

// SetBand saves one radio's settings and reloads Wi-Fi.
func SetBand(in Band) error {
	if err := validate(in.SSID, in.Key, in.Encryption); err != nil {
		return err
	}
	if env.Mock {
		mockMu.Lock()
		defer mockMu.Unlock()
		for i := range mockState.Bands {
			if mockState.Bands[i].Band == in.Band {
				ch := mockState.Bands[i].Channels
				mockState.Bands[i] = in
				mockState.Bands[i].Channels = ch
			}
		}
		return nil
	}
	st, err := Get()
	if err != nil {
		return err
	}
	var cur *Band
	for i := range st.Bands {
		if st.Bands[i].Band == in.Band {
			cur = &st.Bands[i]
		}
	}
	if cur == nil {
		return errors.New("band not found")
	}
	b := uci.NewBatch()
	r := "wireless." + cur.Radio + "."
	if in.Enabled {
		b.Set(r+"disabled", "0")
	} else {
		b.Set(r+"disabled", "1")
	}
	ch := in.Channel
	if ch == "" {
		ch = "auto"
	}
	b.Set(r+"channel", ch)
	if in.HTMode != "" {
		b.Set(r+"htmode", in.HTMode)
	}
	if in.TxPower > 0 {
		b.Set(r+"txpower", strconv.Itoa(in.TxPower))
	} else {
		b.Delete(r + "txpower")
	}
	sec := cur.Section
	if sec == "" {
		sec = "default_" + cur.Radio
		b.Set("wireless."+sec, "wifi-iface")
		b.Set("wireless."+sec+".device", cur.Radio)
		b.Set("wireless."+sec+".mode", "ap")
		b.Set("wireless."+sec+".network", "lan")
	}
	p := "wireless." + sec + "."
	b.Set(p+"ssid", in.SSID)
	b.Set(p+"encryption", in.Encryption)
	if in.Encryption == "none" {
		b.Delete(p + "key")
	} else {
		b.Set(p+"key", in.Key)
	}
	if in.Hidden {
		b.Set(p+"hidden", "1")
	} else {
		b.Delete(p + "hidden")
	}
	b.Set(p+"disabled", "0")
	if strings.HasPrefix(in.Encryption, "sae") {
		b.Set(p+"ieee80211w", "1") // management frame protection, needed by WPA3
	}
	if err := b.Commit(); err != nil {
		return err
	}
	return reloadWifi()
}

func reloadWifi() error {
	_, err := env.Run(30*time.Second, "wifi", "reload")
	return err
}

// SetChannel only changes the channel (used by the smart channel picker).
func SetChannel(band string, channel int) error {
	if env.Mock {
		mockMu.Lock()
		defer mockMu.Unlock()
		for i := range mockState.Bands {
			if mockState.Bands[i].Band == band {
				mockState.Bands[i].Channel = strconv.Itoa(channel)
			}
		}
		return nil
	}
	st, err := Get()
	if err != nil {
		return err
	}
	for _, b := range st.Bands {
		if b.Band == band {
			if err := uci.NewBatch().Set("wireless."+b.Radio+".channel", strconv.Itoa(channel)).Commit(); err != nil {
				return err
			}
			return reloadWifi()
		}
	}
	return errors.New("band not found")
}

// SetGuest creates (once) and updates an isolated guest network on all radios.
func SetGuest(g Guest) error {
	if g.Enabled {
		if err := validate(g.SSID, g.Key, g.Encryption); err != nil {
			return err
		}
	}
	if env.Mock {
		mockMu.Lock()
		defer mockMu.Unlock()
		g.Exists = true
		mockState.Guest = g
		return nil
	}
	st, err := Get()
	if err != nil {
		return err
	}
	b := uci.NewBatch()
	if !st.Guest.Exists {
		b.Set("network.br_guest", "device")
		b.Set("network.br_guest.type", "bridge")
		b.Set("network.br_guest.name", "br-guest")
		b.Set("network.guest", "interface")
		b.Set("network.guest.proto", "static")
		b.Set("network.guest.device", "br-guest")
		b.Set("network.guest.ipaddr", "192.168.30.1")
		b.Set("network.guest.netmask", "255.255.255.0")
		b.Set("dhcp.guest", "dhcp")
		b.Set("dhcp.guest.interface", "guest")
		b.Set("dhcp.guest.start", "100")
		b.Set("dhcp.guest.limit", "150")
		b.Set("dhcp.guest.leasetime", "2h")
		b.Set("firewall.separ_guest", "zone")
		b.Set("firewall.separ_guest.name", "guest")
		b.AddList("firewall.separ_guest.network", "guest")
		b.Set("firewall.separ_guest.input", "REJECT")
		b.Set("firewall.separ_guest.output", "ACCEPT")
		b.Set("firewall.separ_guest.forward", "REJECT")
		b.Set("firewall.separ_guest_wan", "forwarding")
		b.Set("firewall.separ_guest_wan.src", "guest")
		b.Set("firewall.separ_guest_wan.dest", "wan")
		b.Set("firewall.separ_guest_dns", "rule")
		b.Set("firewall.separ_guest_dns.name", "Guest-DHCP-DNS")
		b.Set("firewall.separ_guest_dns.src", "guest")
		b.Set("firewall.separ_guest_dns.dest_port", "53 67 68")
		b.Set("firewall.separ_guest_dns.proto", "tcp udp")
		b.Set("firewall.separ_guest_dns.target", "ACCEPT")
	}
	for i, band := range st.Bands {
		sec := "wireless.guest_" + band.Radio
		if i >= len(GuestIfnames) {
			break
		}
		b.Set(sec, "wifi-iface")
		b.Set(sec+".device", band.Radio)
		b.Set(sec+".mode", "ap")
		b.Set(sec+".network", GuestNet)
		b.Set(sec+".ifname", GuestIfnames[i])
		b.Set(sec+".isolate", "1")
		b.Set(sec+".ssid", g.SSID)
		b.Set(sec+".encryption", g.Encryption)
		if g.Encryption == "none" {
			b.Delete(sec + ".key")
		} else {
			b.Set(sec+".key", g.Key)
		}
		if g.Enabled {
			b.Set(sec+".disabled", "0")
		} else {
			b.Set(sec+".disabled", "1")
		}
	}
	if err := b.Commit(); err != nil {
		return err
	}
	if !st.Guest.Exists {
		env.Run(30*time.Second, "/etc/init.d/network", "reload")
		env.Run(30*time.Second, "/etc/init.d/firewall", "reload")
		env.Run(30*time.Second, "/etc/init.d/dnsmasq", "restart")
	}
	return reloadWifi()
}
