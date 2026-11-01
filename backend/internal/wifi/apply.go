package wifi

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"toofanwrt/internal/env"
	"toofanwrt/internal/uci"
)

// Swapped in tests.
var (
	wirelessConf = "/etc/config/wireless"
	statusFn     = wirelessStatus
	reloadFn     = reloadWifi
	commitFn     = func(b *uci.Batch) error { return b.Commit() }
	settle       = 3 * time.Second
)

// IsDFS reports whether a 5 GHz channel needs radar detection: the radio
// listens for a minute (CAC) before it may transmit there.
func IsDFS(channel int) bool { return channel >= 52 && channel <= 144 }

// fixWidth returns a channel width the band and channel can actually use
// (ath10k radios, as in Google WiFi, are Wi-Fi 5: HT/VHT only).
func fixWidth(band, channel, htmode string) string {
	width := strings.TrimLeft(htmode, "HTVE") // "VHT80" -> "80"
	ch, _ := strconv.Atoi(channel)
	if band != "5g" {
		// 2.4 GHz: HT only, 40 MHz at most; channel 14 is 20 MHz only.
		if width != "40" || ch == 14 {
			return "HT20"
		}
		return "HT40"
	}
	switch {
	case htmode == "":
		return ""
	case ch == 165 || ch == 169 || ch == 173: // no 40/80 MHz partner
		return "VHT20"
	case ch == 144 && width == "80":
		return "VHT40"
	}
	if strings.HasPrefix(htmode, "HE") { // not supported by Wi-Fi 5 radios
		return "VHT" + width
	}
	return htmode
}

var countryRe = regexp.MustCompile(`^([A-Z]{2}|00)$`)

type radioStatus struct {
	Up      bool `json:"up"`
	Pending bool `json:"pending"`
	Failed  bool `json:"retry_setup_failed"`
}

func wirelessStatus() (map[string]radioStatus, error) {
	out, err := env.Ubus("network.wireless", "status", "")
	if err != nil {
		return nil, err
	}
	var m map[string]radioStatus
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// applyRadios commits a wireless change, reloads Wi-Fi and waits for the
// given radios to come up. If one does not (a channel the country or
// driver refuses, a bad width), the previous configuration is restored so
// Wi-Fi never stays down, and the error says what happened.
func applyRadios(b *uci.Batch, radios []string, wait time.Duration) error {
	if env.Mock {
		return b.Commit()
	}
	prev, readErr := os.ReadFile(wirelessConf)
	if err := commitFn(b); err != nil {
		return err
	}
	if err := reloadFn(); err != nil {
		return err
	}
	if readErr != nil || len(radios) == 0 {
		return nil
	}
	deadline := time.Now().Add(wait)
	time.Sleep(settle)
	for {
		st, err := statusFn()
		if err != nil {
			return nil // status not available: nothing to verify against
		}
		ok, failed := true, ""
		for _, r := range radios {
			s := st[r]
			if s.Failed {
				failed = r
			}
			if !s.Up || s.Pending {
				ok = false
			}
		}
		if ok {
			return nil
		}
		if failed != "" || time.Now().After(deadline) {
			os.WriteFile(wirelessConf, prev, 0o644)
			reloadFn()
			return fmt.Errorf("the radio did not start with these settings (the channel or width may not be allowed); the previous settings were restored")
		}
		time.Sleep(time.Second)
	}
}

// waitFor is how long a radio may take to come up on a channel.
func waitFor(channel string) time.Duration {
	if ch, _ := strconv.Atoi(channel); IsDFS(ch) {
		return 100 * time.Second // 60 s radar check (CAC) + start-up
	}
	return 30 * time.Second
}

func radioBand(radio string) string {
	if radioIs5G(radio) {
		return "5g"
	}
	return "2g"
}

func radioDisabled(radio string) bool {
	out, _ := env.Run(3*time.Second, "uci", "-q", "get", "wireless."+radio+".disabled")
	return strings.TrimSpace(out) == "1"
}

var errNoRadio = errors.New("radio not found")
