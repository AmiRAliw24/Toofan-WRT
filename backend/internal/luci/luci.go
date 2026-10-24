// Package luci keeps OpenWrt's own web interface out of the way: Separ is
// the router's single interface on port 80, and LuCI can be switched on
// temporarily (until reboot) on AltPort for rare advanced settings.
package luci

import (
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
	Installed bool `json:"installed"`
	Running   bool `json:"running"`
	Port      int  `json:"port"`
}

var (
	mockMu  sync.Mutex
	mockRun bool
)

func Get() Status {
	st := Status{Port: AltPort}
	if env.Mock {
		mockMu.Lock()
		defer mockMu.Unlock()
		st.Installed, st.Running = true, mockRun
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
		mockMu.Lock()
		mockRun = on
		mockMu.Unlock()
		return nil
	}
	if !on {
		_, err := env.Run(15*time.Second, "/etc/init.d/uhttpd", "stop")
		return err
	}
	// Make sure it can't grab port 80 even if someone edited the config.
	err := uci.NewBatch().
		Delete("uhttpd.main.listen_http").
		AddList("uhttpd.main.listen_http", "0.0.0.0:"+strconv.Itoa(AltPort)).
		Delete("uhttpd.main.listen_https").
		Set("uhttpd.main.redirect_https", "0").Commit()
	if err != nil {
		return err
	}
	env.Run(5*time.Second, "/etc/init.d/uhttpd", "disable")
	_, err = env.Run(20*time.Second, "/etc/init.d/uhttpd", "start")
	return err
}
