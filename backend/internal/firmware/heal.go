package firmware

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"toofanwrt/internal/env"
)

// healScript installs the packages the new firmware lacks (an official
// image instead of the custom build, or packages its feed did not have).
// Required ones must succeed; optional ones are best effort.
func healScript() string {
	return `
REQ="` + strings.Join(Required, " ") + `"
OPT="` + strings.Join(Optional, " ") + `"
if command -v apk >/dev/null 2>&1; then
	has() { apk info -e "$1" >/dev/null 2>&1; }; upd() { apk update; }; add() { apk add "$@"; }
else
	has() { opkg list-installed | grep -q "^$1 "; }; upd() { opkg update; }; add() { opkg install "$@"; }
fi
M=""; for p in $REQ; do has "$p" || M="$M $p"; done
O=""; for p in $OPT; do has "$p" || O="$O $p"; done
if [ -n "$M$O" ]; then
	upd >/dev/null 2>&1 || { echo "no access to the package server (downloads.openwrt.org)" >&2; exit 1; }
	if [ -n "$M" ]; then add $M >/dev/null 2>&1 || { echo "could not install:$M" >&2; exit 1; }; fi
	[ -n "$O" ] && add $O >/dev/null 2>&1
fi
[ -x /etc/init.d/rpcd ] && { /etc/init.d/rpcd enable; /etc/init.d/rpcd start; } >/dev/null 2>&1
[ -x /etc/init.d/xray ] && { /etc/init.d/xray stop; /etc/init.d/xray disable; } >/dev/null 2>&1
[ -x /etc/init.d/miniupnpd ] && [ "$(uci -q get upnpd.config.enabled)" != "1" ] && /etc/init.d/miniupnpd disable
echo "installed:$M$O"
exit 0
`
}

// Heal reports the post-upgrade repair.
type Heal struct {
	State string `json:"state"` // idle | running | ok | failed
	Try   int    `json:"try,omitempty"`
	Error string `json:"error,omitempty"`
	Log   string `json:"log,omitempty"`
}

var (
	healMu sync.Mutex
	heal   = Heal{State: "idle"}

	// OnHealed runs after packages were installed (re-apply the firewall,
	// restart the engine).
	OnHealed func()
	// RemoveLuci uninstalls LuCI (set by the caller to luci.Remove).
	RemoveLuci func() error

	healDelay = 45 * time.Second
	healRetry = 5 * time.Minute
	healTries = 12
	healRun   = func() (string, error) { return env.Run(10*time.Minute, "/bin/sh", "-c", healScript()) }
)

func HealStatus() Heal {
	healMu.Lock()
	defer healMu.Unlock()
	return heal
}

func setHeal(f func(h *Heal)) {
	healMu.Lock()
	f(&heal)
	healMu.Unlock()
}

// installedFn lists installed package names (opkg or apk).
var installedFn = func() (map[string]bool, error) {
	out, err := env.Run(30*time.Second, "/bin/sh", "-c",
		"if command -v apk >/dev/null 2>&1; then apk info; else opkg list-installed | cut -d' ' -f1; fi")
	if err != nil {
		return nil, err
	}
	m := map[string]bool{}
	for _, l := range strings.Fields(out) {
		m[l] = true
	}
	return m, nil
}

// MissingRequired lists required packages that are not installed (rpcd
// removed with LuCI, a firmware without the panel's packages, …).
func MissingRequired() ([]string, error) {
	if env.Mock {
		return nil, nil
	}
	have, err := installedFn()
	if err != nil {
		return nil, err
	}
	var miss []string
	for _, p := range Required {
		if !have[p] {
			miss = append(miss, p)
		}
	}
	return miss, nil
}

// MissingPath is written by install.sh when packages could not be installed.
var MissingPath = "/etc/toofanwrt/pkgs-missing"

// AfterBoot finishes an upgrade on the first start of the new firmware, or
// an install that could not download its packages. It is a no-op otherwise.
func AfterBoot() {
	if env.Mock {
		return
	}
	m, err := ReadMarker()
	_, missing := os.Stat(MissingPath)
	pending := (err == nil && !m.Healed) || missing == nil
	go func() {
		time.Sleep(healDelay) // let the WAN come up
		if !pending {
			// Every start: a required package may have gone (rpcd taken
			// away with LuCI, a manual opkg remove …).
			miss, err := MissingRequired()
			if err != nil || len(miss) == 0 {
				return
			}
			env.Logf("required packages missing: %s; installing", strings.Join(miss, " "))
		}
		for i := 1; i <= healTries; i++ {
			if Repair() == nil {
				return
			}
			time.Sleep(healRetry)
		}
	}()
}

// Repair installs missing packages now (also offered as a button).
func Repair() error {
	healMu.Lock()
	if heal.State == "running" {
		healMu.Unlock()
		return fmt.Errorf("already running")
	}
	heal.State, heal.Try, heal.Error = "running", heal.Try+1, ""
	healMu.Unlock()

	out, err := healRun()
	m, merr := ReadMarker()
	if err != nil {
		env.Logf("firmware: post-upgrade repair: %v", err)
		setHeal(func(h *Heal) { h.State, h.Error, h.Log = "failed", err.Error(), out })
		return err
	}
	if merr == nil && !m.Healed && m.RemoveLuci && RemoveLuci != nil {
		if lerr := RemoveLuci(); lerr != nil {
			env.Logf("firmware: removing LuCI: %v", lerr)
		}
	}
	if merr == nil {
		m.Healed, m.HealedAt, m.Result = true, time.Now().Unix(), strings.TrimSpace(out)
		writeMarker(m)
	}
	os.Remove(MissingPath)
	setHeal(func(h *Heal) { h.State, h.Log = "ok", out })
	if OnHealed != nil {
		OnHealed()
	}
	return nil
}
