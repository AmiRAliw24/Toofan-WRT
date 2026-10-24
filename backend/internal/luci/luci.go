// Package luci keeps OpenWrt's own web interface out of the way: Separ is
// the router's single interface on port 80. LuCI can be removed entirely,
// put back from the package feed, or (while installed) switched on
// temporarily (until reboot) on AltPort for rare advanced settings.
package luci

import (
	"errors"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"separ/internal/env"
	"separ/internal/uci"
)

const AltPort = 8081

type Status struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Port      int    `json:"port"`
	Busy      string `json:"busy,omitempty"` // removing | installing
	Error     string `json:"error,omitempty"`
}

var (
	mu       sync.Mutex
	busy     string
	lastErr  string
	mockRun  bool
	mockGone bool
)

func Get() Status {
	mu.Lock()
	st := Status{Port: AltPort, Busy: busy, Error: lastErr}
	mu.Unlock()
	if env.Mock {
		mu.Lock()
		defer mu.Unlock()
		st.Installed, st.Running = !mockGone, mockRun && !mockGone
		return st
	}
	if _, err := os.Stat("/etc/init.d/uhttpd"); err != nil {
		return st
	}
	st.Installed = true
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(AltPort), 500*time.Millisecond)
	if err == nil {
		c.Close()
		st.Running = true
	}
	return st
}

// Set starts or stops uhttpd. It is never enabled at boot, so a reboot
// always returns to a single interface.
func Set(on bool) error {
	if env.Mock {
		mu.Lock()
		mockRun = on
		mu.Unlock()
		return nil
	}
	if !on {
		_, err := env.Run(15*time.Second, "/etc/init.d/uhttpd", "stop")
		return err
	}
	if err := keepOffPort80(); err != nil {
		return err
	}
	_, err := env.Run(20*time.Second, "/etc/init.d/uhttpd", "start")
	return err
}

// keepOffPort80 makes sure uhttpd can't grab port 80, even if someone edited
// its config, and never starts at boot.
func keepOffPort80() error {
	err := uci.NewBatch().
		Delete("uhttpd.main.listen_http").
		AddList("uhttpd.main.listen_http", "0.0.0.0:"+strconv.Itoa(AltPort)).
		Delete("uhttpd.main.listen_https").
		Set("uhttpd.main.redirect_https", "0").Commit()
	env.Run(5*time.Second, "/etc/init.d/uhttpd", "disable")
	return err
}

// removeScript uninstalls LuCI and its web server in one opkg call, so the
// packages that depend on each other go together. Packages that are part
// of the firmware image only disappear from use; a factory reset brings
// them back.
const removeScript = `
[ -x /etc/init.d/uhttpd ] && { /etc/init.d/uhttpd stop; /etc/init.d/uhttpd disable; } 2>/dev/null
P="$(opkg list-installed | cut -d' ' -f1 | grep -E '^(luci|uhttpd|liblucihttp)')"
[ -z "$P" ] && exit 0
opkg remove $P || opkg remove --force-depends $P
rm -rf /www/luci-static /www/cgi-bin/luci /tmp/luci-*
[ -f /www/index.html ] && grep -q luci /www/index.html && rm -f /www/index.html
exit 0
`

const installScript = `
opkg update >/dev/null 2>&1 || { echo "no access to the package server (downloads.openwrt.org)" >&2; exit 1; }
opkg install luci || opkg install luci-light
`

// Remove uninstalls LuCI in the background.
func Remove() error { return background("removing", removeScript, false) }

// Reinstall downloads LuCI again (needs internet) and keeps it off port 80.
func Reinstall() error { return background("installing", installScript, true) }

func background(what, script string, after bool) error {
	mu.Lock()
	if busy != "" {
		mu.Unlock()
		return errors.New("another LuCI operation is running")
	}
	busy, lastErr = what, ""
	mu.Unlock()
	go func() {
		var err error
		if env.Mock {
			time.Sleep(2 * time.Second)
			mu.Lock()
			mockGone, mockRun = what == "removing", false
			mu.Unlock()
		} else {
			_, err = env.Run(6*time.Minute, "/bin/sh", "-c", script)
			if err == nil && after {
				keepOffPort80()
			}
		}
		mu.Lock()
		busy = ""
		if err != nil {
			lastErr = err.Error()
			env.Logf("luci %s: %v", what, err)
		}
		mu.Unlock()
	}()
	return nil
}
