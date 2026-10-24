package gaming

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"separ/internal/env"
	"separ/internal/uci"
)

type Mapping struct {
	Proto   string `json:"proto"`
	ExtPort int    `json:"extPort"`
	IntAddr string `json:"intAddr"`
	IntPort int    `json:"intPort"`
	Desc    string `json:"desc"`
	Expires int64  `json:"expires,omitempty"`
}

type UPnPState struct {
	Installed bool      `json:"installed"`
	Enabled   bool      `json:"enabled"`
	Mappings  []Mapping `json:"mappings"`
}

var (
	mockMu   sync.Mutex
	mockUPnP = UPnPState{Installed: true, Enabled: true, Mappings: []Mapping{
		{Proto: "UDP", ExtPort: 3074, IntAddr: "192.168.1.140", IntPort: 3074, Desc: "PS5 Call of Duty"},
		{Proto: "UDP", ExtPort: 9308, IntAddr: "192.168.1.140", IntPort: 9308, Desc: "PS5 Remote Play"},
	}}
)

func UPnP() UPnPState {
	if env.Mock {
		mockMu.Lock()
		defer mockMu.Unlock()
		return mockUPnP
	}
	st := UPnPState{}
	if _, err := os.Stat("/etc/init.d/miniupnpd"); err != nil {
		return st
	}
	st.Installed = true
	v, _ := env.Run(3*time.Second, "uci", "-q", "get", "upnpd.config.enabled")
	st.Enabled = v == "1"
	lease, _ := env.Run(3*time.Second, "uci", "-q", "get", "upnpd.config.upnp_lease_file")
	if lease == "" {
		lease = "/var/run/miniupnpd.leases"
	}
	if f, err := os.Open(lease); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			// PROTO:EXTPORT:INTADDR:INTPORT:EXPIRES:DESCRIPTION
			p := strings.SplitN(sc.Text(), ":", 6)
			if len(p) < 6 {
				continue
			}
			ext, _ := strconv.Atoi(p[1])
			in, _ := strconv.Atoi(p[3])
			exp, _ := strconv.ParseInt(p[4], 10, 64)
			st.Mappings = append(st.Mappings, Mapping{Proto: p[0], ExtPort: ext, IntAddr: p[2], IntPort: in, Expires: exp, Desc: p[5]})
		}
		f.Close()
	}
	return st
}

func SetUPnP(on bool) error {
	if env.Mock {
		mockMu.Lock()
		mockUPnP.Enabled = on
		mockMu.Unlock()
		return nil
	}
	val := "0"
	if on {
		val = "1"
	}
	b := uci.NewBatch().Set("upnpd.config.enabled", val)
	if on {
		b.Set("upnpd.config.enable_upnp", "1").Set("upnpd.config.enable_natpmp", "1")
	}
	if err := b.Commit(); err != nil {
		return err
	}
	if on {
		env.Run(10*time.Second, "/etc/init.d/miniupnpd", "enable")
		_, err := env.Run(20*time.Second, "/etc/init.d/miniupnpd", "restart")
		return err
	}
	env.Run(20*time.Second, "/etc/init.d/miniupnpd", "stop")
	env.Run(10*time.Second, "/etc/init.d/miniupnpd", "disable")
	return nil
}
