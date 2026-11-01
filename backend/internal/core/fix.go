package core

import (
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"toofanwrt/internal/dnsctl"
	"toofanwrt/internal/env"
	"toofanwrt/internal/firmware"
	"toofanwrt/internal/luci"
	"toofanwrt/internal/proxy"
	"toofanwrt/internal/system"
)

// FixResult tells the page what a repair attempt did.
type FixResult struct {
	ID string `json:"id"`
	// Fixed: the step passes now. Otherwise Reason (a code the page
	// translates) says why the router could not fix it by itself.
	Fixed  bool       `json:"fixed"`
	Action string     `json:"action"`
	Reason string     `json:"reason,omitempty"`
	Detail string     `json:"detail,omitempty"`
	Report DiagReport `json:"report"`
}

var fixMu sync.Mutex

// Fixable lists the steps with a repair; the others (a filtered direct
// route, …) are outside the router's control.
func Fixable(id string) bool {
	switch id {
	case "wan", "gateway", "isp-dns", "domestic", "engine", "tunnel", "tunnel-dns",
		"lan-dns", "iran-direct", "firewall", "packages", "web":
		return true
	}
	return false
}

// Fix tries to repair one failing step, then diagnoses again.
func (c *Core) Fix(id string) (FixResult, error) {
	if !fixMu.TryLock() {
		return FixResult{}, errors.New("a repair is already running")
	}
	defer fixMu.Unlock()
	res := FixResult{ID: id}
	if !Fixable(id) {
		res.Reason = "not-fixable"
		res.Report = c.Diagnose()
		return res, nil
	}
	if env.Mock {
		time.Sleep(2 * time.Second)
		mockMu.Lock()
		mockRepaired = true
		mockMu.Unlock()
		res.Fixed, res.Action = id != "gateway", actionFor(id)
		if !res.Fixed {
			res.Reason = "modem"
		}
		res.Report = c.Diagnose()
		return res, nil
	}
	res.Action = actionFor(id)
	var err error
	switch id {
	case "wan", "gateway", "isp-dns", "domestic":
		err = c.restartWAN()
	case "engine":
		cfg := c.Store.Get()
		if len(cfg.Proxy.Subscriptions) > 0 {
			c.Proxy.UpdateSubscriptions("")
		}
		c.Proxy.Restart()
		c.Proxy.Kick()
		c.waitTunnel(60 * time.Second)
	case "tunnel":
		// The watchdog re-tests and moves to the best working server.
		c.Proxy.Kick()
		c.waitTunnel(60 * time.Second)
	case "tunnel-dns":
		c.Proxy.Restart()
		c.waitTunnel(30 * time.Second)
	case "lan-dns":
		c.RequestApply()
		time.Sleep(2 * time.Second)
		dnsctl.Recover()
	case "iran-direct":
		c.ReloadGeo()
		c.Reapply()
		time.Sleep(3 * time.Second)
		dnsctl.Recover()
		c.Proxy.Restart()
		c.waitTunnel(30 * time.Second)
	case "firewall":
		c.Reapply()
		time.Sleep(4 * time.Second)
	case "packages":
		err = firmware.Repair()
	case "web":
		luci.Guard()
		for _, p := range c.PanelPorts {
			if p == 80 {
				if ferr := luci.FreePort(p); ferr != nil {
					err = ferr
				}
			}
		}
		time.Sleep(6 * time.Second) // the panel retries its port every 5 s
	}
	if err != nil {
		res.Detail = err.Error()
	}
	res.Report = c.Diagnose()
	for _, s := range res.Report.Steps {
		if s.ID == id {
			res.Fixed = s.Status != "fail"
			if !res.Fixed {
				res.Reason = reasonFor(id)
				if res.Detail == "" {
					res.Detail = s.Detail
				}
			}
		}
	}
	return res, nil
}

func actionFor(id string) string {
	switch id {
	case "wan", "gateway", "isp-dns", "domestic":
		return "restart-wan"
	case "engine":
		return "refresh-servers"
	case "tunnel":
		return "switch-server"
	case "tunnel-dns":
		return "restart-engine"
	case "lan-dns":
		return "restart-dns"
	case "iran-direct":
		return "reload-iran"
	case "firewall":
		return "reload-firewall"
	case "packages":
		return "install-packages"
	case "web":
		return "free-port"
	}
	return ""
}

// reasonFor says what is left for the owner when the repair did not help.
func reasonFor(id string) string {
	switch id {
	case "wan":
		return "cable"
	case "gateway", "isp-dns", "domestic":
		return "modem"
	case "engine", "tunnel":
		return "servers"
	case "tunnel-dns":
		return "dns-provider"
	case "packages":
		return "package-server"
	case "web":
		return "other-program"
	}
	return "unknown"
}

// restartWAN brings the internet interfaces down and up again (a new DHCP
// lease, a fresh PPPoE session) and waits for them.
func (c *Core) restartWAN() error {
	for _, n := range []string{"wan", "wwan"} {
		if _, err := env.Ubus("network.interface."+n, "status", ""); err == nil {
			env.Run(15*time.Second, "/sbin/ifup", n)
		}
	}
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		if st, err := system.WANStatus(); err == nil && st.Up && len(st.IPv4) > 0 {
			time.Sleep(3 * time.Second) // routes and DNS follow the address
			return nil
		}
	}
	return errors.New("the internet interface did not come back up")
}

func (c *Core) waitTunnel(d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		if c.Proxy.Mode() == proxy.ModeConnected {
			return
		}
	}
}

// ---- extra diagnosis steps ----

// packagesStep: every package the panel needs is installed.
func packagesStep() (string, error) {
	miss, err := firmware.MissingRequired()
	if err != nil {
		return "", nil // can't tell (package manager busy): don't blame it
	}
	if len(miss) > 0 {
		return "", errString("missing packages: " + strings.Join(miss, " "))
	}
	return "", nil
}

// webStep: the panel itself answers on port 80.
func (c *Core) webStep() (string, error) {
	has80 := false
	for _, p := range c.PanelPorts {
		has80 = has80 || p == 80
	}
	if !has80 {
		return "", nil
	}
	pid, name := luci.PortHolder(80)
	if pid != 0 && pid != os.Getpid() {
		return "", errString("port 80 is held by " + name)
	}
	if pid == 0 {
		return "", errString("nothing listens on port 80")
	}
	return "", nil
}
