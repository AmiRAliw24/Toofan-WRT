package wifi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"toofanwrt/internal/env"
	"toofanwrt/internal/uci"
)

// SSID is one access point network on the home LAN. Several can share a
// radio (e.g. a second 5 GHz network), and each can be routed through the
// tunnel or directly (handled by the firewall using Netdev).
type SSID struct {
	Section    string `json:"section"`
	Radio      string `json:"radio"`
	Band       string `json:"band"`
	SSID       string `json:"ssid"`
	Encryption string `json:"encryption"`
	Key        string `json:"key"`
	Hidden     bool   `json:"hidden"`
	Enabled    bool   `json:"enabled"`
	Primary    bool   `json:"primary"` // the band's main network (edited on its own tab)
	Netdev     string `json:"netdev,omitempty"`

	Isolate     bool     `json:"isolate"`     // clients can't see each other
	MaxClients  int      `json:"maxClients"`  // 0 = unlimited
	FastRoaming bool     `json:"fastRoaming"` // 802.11r between access points
	MFP         string   `json:"mfp"`         // "0" off, "1" optional, "2" required
	MACFilter   string   `json:"macFilter"`   // "" off | "allow" | "deny"
	MACList     []string `json:"macList"`

	Live *Live `json:"live,omitempty"`
}

// Live is what the radio reports right now for a network.
type Live struct {
	Channel   int    `json:"channel"`
	Frequency int    `json:"frequency"`
	TxPower   int    `json:"txpower"`
	HTMode    string `json:"htmode"`
	BSSID     string `json:"bssid"`
	Clients   int    `json:"clients"`
	Up        bool   `json:"up"`
}

// liveInfo asks iwinfo about a running interface.
func liveInfo(dev string) *Live {
	if dev == "" {
		return nil
	}
	raw, err := env.Ubus("iwinfo", "info", `{"device":"`+dev+`"}`)
	if err != nil {
		return &Live{}
	}
	var i struct {
		Channel   int    `json:"channel"`
		Frequency int    `json:"frequency"`
		TxPower   int    `json:"txpower"`
		HTMode    string `json:"htmode"`
		BSSID     string `json:"bssid"`
	}
	json.Unmarshal([]byte(raw), &i)
	l := &Live{Channel: i.Channel, Frequency: i.Frequency, TxPower: i.TxPower, HTMode: i.HTMode, BSSID: strings.ToLower(i.BSSID), Up: i.Channel > 0}
	if a, err := env.Ubus("iwinfo", "assoclist", `{"device":"`+dev+`"}`); err == nil {
		var r struct {
			Results []json.RawMessage `json:"results"`
		}
		json.Unmarshal([]byte(a), &r)
		l.Clients = len(r.Results)
	}
	return l
}

var (
	mockNetMu sync.Mutex
	mockNets  = []SSID{
		{Section: "default_radio0", Radio: "radio0", Band: "2g", SSID: "ToofanWrt-Home", Encryption: "sae-mixed", Key: "supersecret123", Enabled: true, Primary: true, Netdev: "phy0-ap0", MFP: "1",
			Live: &Live{Channel: 6, Frequency: 2437, TxPower: 20, HTMode: "HT20", BSSID: "f4:f5:d8:10:20:30", Clients: 3, Up: true}},
		{Section: "default_radio1", Radio: "radio1", Band: "5g", SSID: "ToofanWrt-Home-5G", Encryption: "sae-mixed", Key: "supersecret123", Enabled: true, Primary: true, Netdev: "phy1-ap0", MFP: "1", FastRoaming: true,
			Live: &Live{Channel: 36, Frequency: 5180, TxPower: 23, HTMode: "VHT80", BSSID: "f4:f5:d8:10:20:31", Clients: 2, Up: true}},
	}
)

// Netdevs maps wifi-iface sections to their running network devices.
func Netdevs() map[string]string {
	out := map[string]string{}
	if env.Mock {
		mockNetMu.Lock()
		defer mockNetMu.Unlock()
		for _, n := range mockNets {
			out[n.Section] = n.Netdev
		}
		return out
	}
	raw, err := env.Ubus("network.wireless", "status", "")
	if err != nil {
		return out
	}
	var st map[string]struct {
		Interfaces []struct {
			Section string `json:"section"`
			Ifname  string `json:"ifname"`
		} `json:"interfaces"`
	}
	json.Unmarshal([]byte(raw), &st)
	for _, r := range st {
		for _, i := range r.Interfaces {
			if i.Ifname != "" {
				out[i.Section] = i.Ifname
			}
		}
	}
	return out
}

// Networks lists the home (lan) access point networks.
func Networks() ([]SSID, error) {
	if env.Mock {
		mockNetMu.Lock()
		defer mockNetMu.Unlock()
		return append([]SSID{}, mockNets...), nil
	}
	st, err := Get()
	if err != nil {
		return nil, err
	}
	bandOf := map[string]string{}
	primary := map[string]bool{}
	for _, b := range st.Bands {
		bandOf[b.Radio] = b.Band
		primary[b.Section] = true
	}
	m, err := uci.Show("wireless")
	if err != nil {
		return nil, err
	}
	devs := Netdevs()
	var out []SSID
	for k, v := range m {
		parts := strings.Split(k, ".")
		if len(parts) != 2 || v != "wifi-iface" {
			continue
		}
		sec := parts[1]
		p := "wireless." + sec + "."
		if m[p+"mode"] != "ap" || m[p+"network"] == GuestNet {
			continue
		}
		radio := m[p+"device"]
		out = append(out, SSID{
			Section: sec, Radio: radio, Band: bandOf[radio], SSID: m[p+"ssid"], Encryption: m[p+"encryption"],
			Key: m[p+"key"], Hidden: m[p+"hidden"] == "1",
			Enabled: m[p+"disabled"] != "1" && m["wireless."+radio+".disabled"] != "1",
			Primary: primary[sec], Netdev: devs[sec],
			Isolate: m[p+"isolate"] == "1", FastRoaming: m[p+"ieee80211r"] == "1",
			MFP: m[p+"ieee80211w"], MACFilter: m[p+"macfilter"],
			MACList: strings.Fields(strings.ReplaceAll(m[p+"maclist"], "'", "")),
			Live:    liveInfo(devs[sec]),
		})
		n := &out[len(out)-1]
		n.MaxClients, _ = strconv.Atoi(m[p+"maxassoc"])
		if n.MACFilter == "disable" {
			n.MACFilter = ""
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Primary != out[j].Primary {
			return out[i].Primary
		}
		if out[i].Band != out[j].Band {
			return out[i].Band < out[j].Band
		}
		return out[i].Section < out[j].Section
	})
	return out, nil
}

func newSection() string {
	b := make([]byte, 3)
	rand.Read(b)
	return "separ_ssid_" + hex.EncodeToString(b)
}

// SaveNetwork creates (empty Section) or updates an extra network and
// returns its section name(s). Band "both" creates one per radio.
var macRe = regexp.MustCompile(`^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$`)

func SaveNetwork(n SSID) ([]string, error) {
	if err := validate(n.SSID, n.Key, n.Encryption); err != nil {
		return nil, err
	}
	if n.MaxClients < 0 || n.MaxClients > 128 {
		return nil, errors.New("max clients must be 0-128")
	}
	for _, m := range n.MACList {
		if !macRe.MatchString(m) {
			return nil, errors.New("invalid MAC address: " + m)
		}
	}
	if (n.MACFilter == "allow") && len(n.MACList) == 0 {
		return nil, errors.New("an allow-list needs at least one MAC address")
	}
	if env.Mock {
		mockNetMu.Lock()
		defer mockNetMu.Unlock()
		if n.Section != "" {
			for i := range mockNets {
				if mockNets[i].Section == n.Section {
					keep := mockNets[i]
					n.Radio, n.Band, n.Primary, n.Netdev, n.Live = keep.Radio, keep.Band, keep.Primary, keep.Netdev, keep.Live
					mockNets[i] = n
				}
			}
			return []string{n.Section}, nil
		}
		var secs []string
		for _, band := range bandsFor(n.Band) {
			sec := newSection()
			idx := len(mockNets)
			mockNets = append(mockNets, SSID{Section: sec, Radio: map[string]string{"2g": "radio0", "5g": "radio1"}[band], Band: band,
				SSID: n.SSID, Encryption: n.Encryption, Key: n.Key, Hidden: n.Hidden, Enabled: true, Netdev: "phy" + map[string]string{"2g": "0", "5g": "1"}[band] + "-ap" + string(rune('0'+idx))})
			secs = append(secs, sec)
		}
		return secs, nil
	}
	st, err := Get()
	if err != nil {
		return nil, err
	}
	radioFor := map[string]string{}
	for _, b := range st.Bands {
		radioFor[b.Band] = b.Radio
	}
	b := uci.NewBatch()
	var secs []string
	put := func(sec string) {
		p := "wireless." + sec + "."
		b.Set(p+"ssid", n.SSID)
		b.Set(p+"encryption", n.Encryption)
		if n.Encryption == "none" {
			b.Delete(p + "key")
		} else {
			b.Set(p+"key", n.Key)
		}
		if n.Hidden {
			b.Set(p+"hidden", "1")
		} else {
			b.Delete(p + "hidden")
		}
		mfp := n.MFP
		if strings.HasPrefix(n.Encryption, "sae") && mfp != "2" {
			mfp = "1" // WPA3 needs management frame protection
		}
		if n.Encryption == "none" {
			mfp = "0"
		}
		switch mfp {
		case "1", "2":
			b.Set(p+"ieee80211w", mfp)
		default:
			b.Delete(p + "ieee80211w")
		}
		setBool := func(opt string, on bool) {
			if on {
				b.Set(p+opt, "1")
			} else {
				b.Delete(p + opt)
			}
		}
		setBool("isolate", n.Isolate)
		setBool("ieee80211r", n.FastRoaming)
		if n.FastRoaming {
			b.Set(p+"ft_over_ds", "0")
			b.Set(p+"ft_psk_generate_local", "1")
		}
		if n.MaxClients > 0 {
			b.Set(p+"maxassoc", strconv.Itoa(n.MaxClients))
		} else {
			b.Delete(p + "maxassoc")
		}
		b.Delete(p + "maclist")
		if n.MACFilter == "allow" || n.MACFilter == "deny" {
			b.Set(p+"macfilter", n.MACFilter)
			for _, mac := range n.MACList {
				b.AddList(p+"maclist", strings.ToLower(mac))
			}
		} else {
			b.Delete(p + "macfilter")
		}
		if n.Enabled {
			b.Set(p+"disabled", "0")
		} else {
			b.Set(p+"disabled", "1")
		}
	}
	if n.Section != "" {
		nets, err := Networks()
		if err != nil {
			return nil, err
		}
		found := false
		for _, x := range nets {
			found = found || x.Section == n.Section
		}
		if !found {
			return nil, errors.New("network not found")
		}
		put(n.Section)
		secs = []string{n.Section}
	} else {
		for _, band := range bandsFor(n.Band) {
			radio := radioFor[band]
			if radio == "" {
				return nil, errors.New("this router has no " + band + " radio")
			}
			sec := newSection()
			b.Set("wireless."+sec, "wifi-iface")
			b.Set("wireless."+sec+".device", radio)
			b.Set("wireless."+sec+".mode", "ap")
			b.Set("wireless."+sec+".network", "lan")
			b.Set("wireless."+radio+".disabled", "0")
			n.Enabled = true
			put(sec)
			secs = append(secs, sec)
		}
	}
	if err := b.Commit(); err != nil {
		return nil, err
	}
	_, err = env.Run(30*time.Second, "wifi", "reload")
	return secs, err
}

func bandsFor(b string) []string {
	if b == "both" {
		return []string{"2g", "5g"}
	}
	return []string{b}
}

// DeleteNetwork removes an extra network (primary ones are only disabled).
func DeleteNetwork(section string) error {
	if env.Mock {
		mockNetMu.Lock()
		defer mockNetMu.Unlock()
		for i := range mockNets {
			if mockNets[i].Section == section {
				if mockNets[i].Primary {
					return errors.New("the main network can only be turned off")
				}
				mockNets = append(mockNets[:i], mockNets[i+1:]...)
				return nil
			}
		}
		return errors.New("network not found")
	}
	nets, err := Networks()
	if err != nil {
		return err
	}
	for _, n := range nets {
		if n.Section == section {
			if n.Primary {
				return errors.New("the main network can only be turned off")
			}
			if err := uci.NewBatch().Delete("wireless." + section).Commit(); err != nil {
				return err
			}
			_, err := env.Run(30*time.Second, "wifi", "reload")
			return err
		}
	}
	return errors.New("network not found")
}

// RadioSettings are shared by every network on a band.
type RadioSettings struct {
	Radio   string `json:"radio"`
	Channel string `json:"channel"` // "auto" or number
	HTMode  string `json:"htmode"`  // channel width
	TxPower int    `json:"txpower"` // dBm, 0 = maximum allowed
}

var htModes = map[string]bool{"HT20": true, "HT40": true, "VHT20": true, "VHT40": true, "VHT80": true, "VHT160": true, "HE20": true, "HE40": true, "HE80": true}

func SetRadio(r RadioSettings) error {
	if r.HTMode != "" && !htModes[r.HTMode] {
		return errors.New("invalid channel width")
	}
	if r.Channel != "auto" {
		if n, err := strconv.Atoi(r.Channel); err != nil || n < 1 || n > 196 {
			return errors.New("invalid channel")
		}
	}
	if r.TxPower < 0 || r.TxPower > 30 {
		return errors.New("transmit power must be 0-30 dBm")
	}
	if env.Mock {
		return nil
	}
	if _, err := env.Run(3*time.Second, "uci", "-q", "get", "wireless."+r.Radio); err != nil {
		return errors.New("radio not found")
	}
	b := uci.NewBatch().Set("wireless."+r.Radio+".channel", r.Channel)
	if r.HTMode != "" {
		b.Set("wireless."+r.Radio+".htmode", r.HTMode)
	}
	if r.TxPower > 0 {
		b.Set("wireless."+r.Radio+".txpower", strconv.Itoa(r.TxPower))
	} else {
		b.Delete("wireless." + r.Radio + ".txpower")
	}
	if err := b.Commit(); err != nil {
		return err
	}
	_, err := env.Run(30*time.Second, "wifi", "reload")
	return err
}
