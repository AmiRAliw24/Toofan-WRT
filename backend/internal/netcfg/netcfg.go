// Package netcfg edits the basic network settings people otherwise need
// LuCI for: the internet connection, the LAN address and the clock.
package netcfg

import (
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"toofanwrt/internal/env"
	"toofanwrt/internal/uci"
)

type WAN struct {
	Proto    string   `json:"proto"` // dhcp | pppoe | static
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	IPAddr   string   `json:"ipaddr,omitempty"`
	Netmask  string   `json:"netmask,omitempty"`
	Gateway  string   `json:"gateway,omitempty"`
	DNS      []string `json:"dns,omitempty"` // custom DNS (empty = from ISP)
}

type LAN struct {
	IPAddr    string `json:"ipaddr"`
	Netmask   string `json:"netmask"`
	DHCPStart int    `json:"dhcpStart"`
	DHCPLimit int    `json:"dhcpLimit"`
	LeaseTime string `json:"leaseTime"`
}

type System struct {
	Hostname string `json:"hostname"`
	Zone     string `json:"zone"`
}

type Settings struct {
	WAN    WAN      `json:"wan"`
	LAN    LAN      `json:"lan"`
	System System   `json:"system"`
	Zones  []string `json:"zones"`
}

// zones maps the offered time zones to their POSIX TZ strings.
var zones = map[string]string{
	"Asia/Tehran":      "<+0330>-3:30",
	"UTC":              "UTC0",
	"Asia/Dubai":       "<+04>-4",
	"Europe/Istanbul":  "<+03>-3",
	"Europe/Berlin":    "CET-1CEST,M3.5.0,M10.5.0/3",
	"Europe/London":    "GMT0BST,M3.5.0/1,M10.5.0",
	"America/New_York": "EST5EDT,M3.2.0,M11.1.0",
	"America/Toronto":  "EST5EDT,M3.2.0,M11.1.0",
	"Asia/Kabul":       "<+0430>-4:30",
}

func zoneList() []string {
	return []string{"Asia/Tehran", "Asia/Dubai", "Europe/Istanbul", "Asia/Kabul", "Europe/Berlin", "Europe/London", "America/New_York", "America/Toronto", "UTC"}
}

var (
	mockMu sync.Mutex
	mock   = Settings{
		WAN:    WAN{Proto: "dhcp"},
		LAN:    LAN{IPAddr: "192.168.1.1", Netmask: "255.255.255.0", DHCPStart: 100, DHCPLimit: 150, LeaseTime: "12h"},
		System: System{Hostname: "Google", Zone: "Asia/Tehran"},
	}
)

func Get() (Settings, error) {
	if env.Mock {
		mockMu.Lock()
		defer mockMu.Unlock()
		s := mock
		s.Zones = zoneList()
		s.WAN.Password = ""
		return s, nil
	}
	n, err := uci.Show("network")
	if err != nil {
		return Settings{}, err
	}
	d, _ := uci.Show("dhcp")
	sys, _ := uci.Show("system")
	s := Settings{Zones: zoneList()}
	s.WAN = WAN{
		Proto: n["network.wan.proto"], Username: n["network.wan.username"],
		IPAddr: n["network.wan.ipaddr"], Netmask: n["network.wan.netmask"], Gateway: n["network.wan.gateway"],
	}
	if v := n["network.wan.dns"]; v != "" {
		s.WAN.DNS = strings.Fields(strings.ReplaceAll(v, "'", ""))
	}
	ip := n["network.lan.ipaddr"]
	mask := n["network.lan.netmask"]
	if strings.Contains(ip, "/") { // 23.05 may store CIDR
		if p, ipn, err := net.ParseCIDR(ip); err == nil {
			ip, mask = p.String(), net.IP(ipn.Mask).String()
		}
	}
	s.LAN = LAN{IPAddr: ip, Netmask: mask, LeaseTime: d["dhcp.lan.leasetime"]}
	s.LAN.DHCPStart, _ = strconv.Atoi(d["dhcp.lan.start"])
	s.LAN.DHCPLimit, _ = strconv.Atoi(d["dhcp.lan.limit"])
	for k, v := range sys {
		if strings.HasSuffix(k, ".hostname") {
			s.System.Hostname = v
		}
		if strings.HasSuffix(k, ".zonename") {
			s.System.Zone = v
		}
	}
	return s, nil
}

func validIP(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}

func SetWAN(w WAN) error {
	switch w.Proto {
	case "dhcp":
	case "pppoe":
		if w.Username == "" {
			return errors.New("PPPoE needs a username")
		}
	case "static":
		if !validIP(w.IPAddr) || !validIP(w.Netmask) || !validIP(w.Gateway) {
			return errors.New("enter a valid IP, netmask and gateway")
		}
	default:
		return errors.New("unknown connection type")
	}
	for _, d := range w.DNS {
		if !validIP(d) {
			return errors.New("invalid DNS address")
		}
	}
	if env.Mock {
		mockMu.Lock()
		mock.WAN = w
		mockMu.Unlock()
		return nil
	}
	b := uci.NewBatch().Set("network.wan.proto", w.Proto)
	for _, k := range []string{"username", "password", "ipaddr", "netmask", "gateway", "dns"} {
		b.Delete("network.wan." + k)
	}
	switch w.Proto {
	case "pppoe":
		b.Set("network.wan.username", w.Username)
		if w.Password != "" {
			b.Set("network.wan.password", w.Password)
		} else if cur, _ := env.Run(3*time.Second, "uci", "-q", "get", "network.wan.password"); cur != "" {
			b.Set("network.wan.password", cur) // keep the saved one
		}
	case "static":
		b.Set("network.wan.ipaddr", w.IPAddr).Set("network.wan.netmask", w.Netmask).Set("network.wan.gateway", w.Gateway)
	}
	if len(w.DNS) > 0 {
		b.Set("network.wan.peerdns", "0")
		for _, d := range w.DNS {
			b.AddList("network.wan.dns", d)
		}
	} else {
		b.Delete("network.wan.peerdns")
	}
	if err := b.Commit(); err != nil {
		return err
	}
	_, err := env.Run(30*time.Second, "/etc/init.d/network", "reload")
	return err
}

var leaseRe = regexp.MustCompile(`^\d+[mhd]$`)

// SetLAN changes the router's own address; the network restarts a few
// seconds after the response so the browser can follow to the new IP.
func SetLAN(l LAN) error {
	if !validIP(l.IPAddr) || !validIP(l.Netmask) {
		return errors.New("enter a valid IP and netmask")
	}
	ip := net.ParseIP(l.IPAddr).To4()
	if !ip.IsPrivate() {
		return errors.New("use a private address (192.168.x.x, 10.x.x.x or 172.16-31.x.x)")
	}
	if l.DHCPStart < 2 || l.DHCPStart > 250 || l.DHCPLimit < 1 || l.DHCPStart+l.DHCPLimit > 255 {
		return errors.New("invalid DHCP range")
	}
	if !leaseRe.MatchString(l.LeaseTime) {
		return errors.New("lease time like 12h, 30m or 1d")
	}
	if env.Mock {
		mockMu.Lock()
		mock.LAN = l
		mockMu.Unlock()
		return nil
	}
	err := uci.NewBatch().
		Set("network.lan.ipaddr", l.IPAddr).Set("network.lan.netmask", l.Netmask).
		Set("dhcp.lan.start", strconv.Itoa(l.DHCPStart)).Set("dhcp.lan.limit", strconv.Itoa(l.DHCPLimit)).
		Set("dhcp.lan.leasetime", l.LeaseTime).Commit()
	if err != nil {
		return err
	}
	go func() {
		time.Sleep(2 * time.Second)
		env.Run(60*time.Second, "/etc/init.d/network", "restart")
		env.Run(30*time.Second, "/etc/init.d/dnsmasq", "restart")
	}()
	return nil
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,62}$`)

func SetSystem(s System) error {
	tz, ok := zones[s.Zone]
	if !ok {
		return errors.New("unknown time zone")
	}
	if !hostRe.MatchString(s.Hostname) {
		return errors.New("hostname: letters, digits and '-' only")
	}
	if env.Mock {
		mockMu.Lock()
		mock.System = s
		mockMu.Unlock()
		return nil
	}
	err := uci.NewBatch().Set("system.@system[0].hostname", s.Hostname).
		Set("system.@system[0].zonename", s.Zone).Set("system.@system[0].timezone", tz).Commit()
	if err != nil {
		return err
	}
	_, err = env.Run(15*time.Second, "/etc/init.d/system", "reload")
	return err
}
