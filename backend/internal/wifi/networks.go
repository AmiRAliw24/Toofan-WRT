package wifi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"separ/internal/env"
	"separ/internal/uci"
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
}

var (
	mockNetMu sync.Mutex
	mockNets  = []SSID{
		{Section: "default_radio0", Radio: "radio0", Band: "2g", SSID: "Separ-Home", Encryption: "sae-mixed", Key: "supersecret123", Enabled: true, Primary: true, Netdev: "phy0-ap0"},
		{Section: "default_radio1", Radio: "radio1", Band: "5g", SSID: "Separ-Home-5G", Encryption: "sae-mixed", Key: "supersecret123", Enabled: true, Primary: true, Netdev: "phy1-ap0"},
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
		})
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
func SaveNetwork(n SSID) ([]string, error) {
	if err := validate(n.SSID, n.Key, n.Encryption); err != nil {
		return nil, err
	}
	if env.Mock {
		mockNetMu.Lock()
		defer mockNetMu.Unlock()
		if n.Section != "" {
			for i := range mockNets {
				if mockNets[i].Section == n.Section {
					keep := mockNets[i]
					n.Radio, n.Band, n.Primary, n.Netdev = keep.Radio, keep.Band, keep.Primary, keep.Netdev
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
		if strings.HasPrefix(n.Encryption, "sae") {
			b.Set(p+"ieee80211w", "1")
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
