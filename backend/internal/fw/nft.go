// Package fw renders and applies the panel's own nftables table.
// It lives next to fw4 (table inet separ) so firewall reloads don't touch it.
package fw

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"separ/internal/env"
)

const (
	Table      = "separ"
	ProxyMark  = 1
	RouteTable = 100
)

type DeviceRule struct {
	MAC      string
	IP       string
	DownKbps int
	UpKbps   int
}

type ScheduleRule struct {
	MACs []string
	Days []int
	From string // HH:MM
	To   string
}

type Plan struct {
	LANIfaces   []string // traffic from these is proxied
	AllIfaces   []string // every local client interface (for blocks/accounting)
	TProxy      bool     // capture traffic to Xray
	TProxyPort  int
	IranCIDRs   []string // bypassed at kernel level when non-empty
	BypassMACs  []string
	BlockMACs   []string
	BlockIPv6   bool
	Devices     []DeviceRule
	Schedules   []ScheduleRule
	PriorityIPs []string // marked with DSCP EF for queueing
	LockWAN     bool     // drop panel/SSH/LuCI connections arriving from the WAN
	WANDev      string
	PanelPorts  []int
}

var privateNets = []string{
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4",
}

func quoteList(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = fmt.Sprintf("%q", x)
	}
	return strings.Join(q, ", ")
}

func set(name, typ string, interval bool, elems []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\tset %s {\n\t\ttype %s\n", name, typ)
	if interval {
		b.WriteString("\t\tflags interval\n\t\tauto-merge\n")
	}
	if len(elems) > 0 {
		fmt.Fprintf(&b, "\t\telements = { %s }\n", strings.Join(elems, ", "))
	}
	b.WriteString("\t}\n")
	return b.String()
}

// Render produces an nft script that atomically replaces the table.
func Render(p Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "table inet %s\ndelete table inet %s\ntable inet %s {\n", Table, Table, Table)
	b.WriteString(set("private4", "ipv4_addr", true, privateNets))
	b.WriteString(set("iran4", "ipv4_addr", true, p.IranCIDRs))
	b.WriteString(set("bypass_mac", "ether_addr", false, p.BypassMACs))
	b.WriteString(set("block_mac", "ether_addr", false, p.BlockMACs))

	all := p.AllIfaces
	if len(all) == 0 {
		all = p.LANIfaces
	}
	allIf := "{ " + quoteList(all) + " }"

	// Guard: blocks, schedules and IPv6 before anything else sees the packet.
	b.WriteString("\tchain guard {\n\t\ttype filter hook prerouting priority mangle - 10; policy accept;\n")
	if len(all) > 0 {
		fmt.Fprintf(&b, "\t\tiifname != %s return\n", allIf)
		b.WriteString("\t\tudp dport { 67, 68 } accept\n")
		b.WriteString("\t\tether saddr @block_mac drop\n")
		for _, s := range p.Schedules {
			for _, r := range scheduleRanges(s) {
				fmt.Fprintf(&b, "\t\tether saddr { %s } %s drop\n", strings.Join(s.MACs, ", "), r)
			}
		}
		for _, d := range p.Devices {
			if d.UpKbps > 0 && d.IP != "" {
				fmt.Fprintf(&b, "\t\tip saddr %s ip daddr != @private4 limit rate over %d kbytes/second burst %d kbytes drop\n",
					d.IP, d.UpKbps/8, max(d.UpKbps/8, 64))
			}
		}
		for _, ip := range p.PriorityIPs {
			fmt.Fprintf(&b, "\t\tip saddr %s ip dscp set ef\n", ip)
		}
	}
	b.WriteString("\t}\n")

	if p.BlockIPv6 && len(all) > 0 {
		b.WriteString("\tchain no_ipv6 {\n\t\ttype filter hook forward priority filter - 5; policy accept;\n")
		fmt.Fprintf(&b, "\t\tmeta nfproto ipv6 iifname %s oifname != %s reject with icmpv6 type admin-prohibited\n", allIf, allIf)
		b.WriteString("\t}\n")
	}

	if p.LockWAN && p.WANDev != "" {
		ports := []string{"22", "80", "443"}
		for _, pt := range p.PanelPorts {
			ports = append(ports, fmt.Sprint(pt))
		}
		b.WriteString("\tchain wan_lock {\n\t\ttype filter hook input priority filter - 5; policy accept;\n")
		fmt.Fprintf(&b, "\t\tiifname %q tcp dport { %s } drop\n", p.WANDev, strings.Join(dedupe(ports), ", "))
		b.WriteString("\t}\n")
	}

	// Transparent proxy capture.
	b.WriteString("\tchain proxy {\n\t\ttype filter hook prerouting priority mangle; policy accept;\n")
	if p.TProxy && len(p.LANIfaces) > 0 {
		fmt.Fprintf(&b, "\t\tiifname != { %s } return\n", quoteList(p.LANIfaces))
		b.WriteString("\t\tmeta nfproto ipv6 return\n")
		b.WriteString("\t\tip daddr @private4 return\n")
		b.WriteString("\t\tether saddr @bypass_mac return\n")
		if len(p.IranCIDRs) > 0 {
			b.WriteString("\t\tip daddr @iran4 return\n")
		}
		fmt.Fprintf(&b, "\t\tmeta l4proto { tcp, udp } meta mark set %d tproxy ip to 127.0.0.1:%d accept\n", ProxyMark, p.TProxyPort)
	}
	b.WriteString("\t}\n")

	// Per-device byte counters: upload on ingress, download on egress.
	b.WriteString("\tchain acct_up {\n\t\ttype filter hook prerouting priority mangle - 20; policy accept;\n")
	for _, d := range p.Devices {
		if d.IP != "" {
			fmt.Fprintf(&b, "\t\tip saddr %s ip daddr != @private4 counter comment \"u %s\"\n", d.IP, d.IP)
		}
	}
	b.WriteString("\t}\n")
	b.WriteString("\tchain acct_down {\n\t\ttype filter hook postrouting priority mangle; policy accept;\n")
	for _, d := range p.Devices {
		if d.IP == "" {
			continue
		}
		if d.DownKbps > 0 {
			fmt.Fprintf(&b, "\t\tip daddr %s ip saddr != @private4 limit rate over %d kbytes/second burst %d kbytes drop\n",
				d.IP, d.DownKbps/8, max(d.DownKbps/8, 64))
		}
		fmt.Fprintf(&b, "\t\tip daddr %s ip saddr != @private4 counter comment \"d %s\"\n", d.IP, d.IP)
	}
	b.WriteString("\t}\n")
	b.WriteString("}\n")
	return b.String()
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// scheduleRanges converts a block window to nft time matches, splitting
// windows that cross midnight. The second half belongs to the next day.
func scheduleRanges(s ScheduleRule) []string {
	days := func(shift int) string {
		if len(s.Days) == 0 || len(s.Days) == 7 {
			return ""
		}
		ds := make([]string, 0, len(s.Days))
		for _, d := range s.Days {
			ds = append(ds, fmt.Sprint((d+shift)%7))
		}
		sort.Strings(ds)
		return "meta day { " + strings.Join(ds, ", ") + " } "
	}
	if s.From == s.To {
		return []string{days(0)}
	}
	if s.From < s.To {
		return []string{fmt.Sprintf("%smeta hour %q-%q", days(0), s.From, s.To)}
	}
	return []string{
		fmt.Sprintf("%smeta hour %q-\"23:59:59\"", days(0), s.From),
		fmt.Sprintf("%smeta hour \"00:00\"-%q", days(1), s.To),
	}
}

// Apply loads the plan and makes sure policy routing for TProxy exists.
func Apply(p Plan) error {
	script := Render(p)
	if env.Mock {
		return nil
	}
	if _, err := env.RunInput(20*time.Second, script, "nft", "-f", "-"); err != nil {
		return err
	}
	ensurePolicyRoute()
	return nil
}

func ensurePolicyRoute() {
	rules, _ := env.Run(5*time.Second, "ip", "rule", "show")
	if !strings.Contains(rules, fmt.Sprintf("fwmark 0x%x lookup %d", ProxyMark, RouteTable)) {
		env.Run(5*time.Second, "ip", "rule", "add", "fwmark", fmt.Sprint(ProxyMark), "table", fmt.Sprint(RouteTable))
	}
	env.Run(5*time.Second, "ip", "route", "replace", "local", "0.0.0.0/0", "dev", "lo", "table", fmt.Sprint(RouteTable))
}

// Remove deletes the table (used on uninstall / panel stop).
func Remove() {
	if env.Mock {
		return
	}
	env.Run(10*time.Second, "nft", "delete", "table", "inet", Table)
}

// Exists reports whether our table is still loaded (fw4 restarts can't
// remove it, but a manual "nft flush ruleset" can).
func Exists() bool {
	if env.Mock {
		return true
	}
	_, err := env.Run(5*time.Second, "nft", "list", "table", "inet", Table)
	return err == nil
}

// Counters returns per-IP byte totals: up and down.
func Counters() (up, down map[string]uint64, err error) {
	up, down = map[string]uint64{}, map[string]uint64{}
	if env.Mock {
		return
	}
	out, err := env.Run(10*time.Second, "nft", "-j", "list", "table", "inet", Table)
	if err != nil {
		return
	}
	var doc struct {
		Nftables []struct {
			Rule *struct {
				Comment string            `json:"comment"`
				Expr    []json.RawMessage `json:"expr"`
			} `json:"rule"`
		} `json:"nftables"`
	}
	if err = json.Unmarshal([]byte(out), &doc); err != nil {
		return
	}
	for _, it := range doc.Nftables {
		if it.Rule == nil || it.Rule.Comment == "" {
			continue
		}
		dir, ip, ok := strings.Cut(it.Rule.Comment, " ")
		if !ok {
			continue
		}
		for _, e := range it.Rule.Expr {
			var c struct {
				Counter *struct {
					Bytes uint64 `json:"bytes"`
				} `json:"counter"`
			}
			if json.Unmarshal(e, &c) == nil && c.Counter != nil {
				if dir == "u" {
					up[ip] = c.Counter.Bytes
				} else {
					down[ip] = c.Counter.Bytes
				}
			}
		}
	}
	return
}
