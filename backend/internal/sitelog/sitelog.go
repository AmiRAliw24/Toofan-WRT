// Package sitelog records which sites each device opens, from dnsmasq's
// query log. Everything stays in memory: it is gone after a reboot.
package sitelog

import (
	"bufio"
	"context"
	"math/rand"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"separ/internal/env"
)

type Site struct {
	Domain  string `json:"domain"`
	Count   int    `json:"count"`
	First   int64  `json:"first"`
	Last    int64  `json:"last"`
	Devices int    `json:"devices,omitempty"` // only in the all-devices view
}

const perDevice = 500 // domains kept per device; the least recent go first

type Log struct {
	// Owner maps a client IP to its MAC ("" when unknown).
	Owner func(ip string) string

	mu      sync.Mutex
	sites   map[string]map[string]*Site // mac (or "ip:x") -> domain -> site
	cancel  context.CancelFunc
	since   int64
	enabled bool
}

func New(owner func(string) string) *Log {
	return &Log{Owner: owner, sites: map[string]map[string]*Site{}}
}

// SetEnabled starts or stops following the log. Turning it off also
// forgets everything recorded.
func (l *Log) SetEnabled(on bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if on == l.enabled {
		return
	}
	l.enabled = on
	if !on {
		if l.cancel != nil {
			l.cancel()
			l.cancel = nil
		}
		l.sites = map[string]map[string]*Site{}
		l.since = 0
		return
	}
	l.since = time.Now().Unix()
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	if env.Mock {
		go l.mock(ctx)
	} else {
		go l.follow(ctx)
	}
}

// Since is when recording started (0 = off).
func (l *Log) Since() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.since
}

var queryRe = regexp.MustCompile(`query\[(?:A|AAAA|HTTPS|TYPE65)\] (\S+) from (\S+)`)

func (l *Log) follow(ctx context.Context) {
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, "logread", "-f", "-e", `query\[`)
		out, err := cmd.StdoutPipe()
		if err == nil && cmd.Start() == nil {
			sc := bufio.NewScanner(out)
			for sc.Scan() {
				if m := queryRe.FindStringSubmatch(sc.Text()); m != nil {
					l.record(m[2], m[1], time.Now().Unix())
				}
			}
			cmd.Wait()
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second): // logd restarted; follow again
		}
	}
}

func skipDomain(d string) bool {
	if !strings.Contains(d, ".") {
		return true // local names
	}
	for _, suf := range []string{".lan", ".local", ".arpa", ".localdomain", ".home"} {
		if strings.HasSuffix(d, suf) {
			return true
		}
	}
	return false
}

func (l *Log) record(ip, domain string, now int64) {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	if ip == "127.0.0.1" || ip == "::1" || skipDomain(domain) {
		return
	}
	key := ""
	if l.Owner != nil {
		key = l.Owner(ip)
	}
	if key == "" {
		key = "ip:" + ip
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.enabled {
		return
	}
	m := l.sites[key]
	if m == nil {
		m = map[string]*Site{}
		l.sites[key] = m
	}
	s := m[domain]
	if s == nil {
		s = &Site{Domain: domain, First: now}
		m[domain] = s
	}
	s.Count++
	s.Last = now
	if len(m) > perDevice+100 {
		list := make([]*Site, 0, len(m))
		for _, x := range m {
			list = append(list, x)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Last > list[j].Last })
		for _, x := range list[perDevice:] {
			delete(m, x.Domain)
		}
	}
}

// Sites lists the sites of one device (mac) or, with mac "", of all
// devices together; most recent first.
func (l *Log) Sites(mac string) []Site {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []Site{}
	if mac != "" {
		for _, s := range l.sites[mac] {
			out = append(out, *s)
		}
	} else {
		all := map[string]*Site{}
		for _, m := range l.sites {
			for d, s := range m {
				a := all[d]
				if a == nil {
					a = &Site{Domain: d, First: s.First}
					all[d] = a
				}
				a.Count += s.Count
				a.Devices++
				a.First = min(a.First, s.First)
				a.Last = max(a.Last, s.Last)
			}
		}
		for _, s := range all {
			out = append(out, *s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last > out[j].Last })
	return out
}

// Counts returns how many distinct sites each device opened.
func (l *Log) Counts() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]int{}
	for k, m := range l.sites {
		out[k] = len(m)
	}
	return out
}

// Clear forgets one device's sites, or everything with mac "".
func (l *Log) Clear(mac string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if mac == "" {
		l.sites = map[string]map[string]*Site{}
		return
	}
	delete(l.sites, mac)
}

func (l *Log) mock(ctx context.Context) {
	domains := []string{"www.google.com", "www.youtube.com", "i.ytimg.com", "web.telegram.org", "www.instagram.com",
		"www.digikala.com", "www.aparat.com", "api.snapp.ir", "github.com", "www.wikipedia.org", "open.spotify.com",
		"www.netflix.com", "graph.facebook.com", "play.googleapis.com", "time.apple.com", "divar.ir"}
	ips := []string{"192.168.1.238", "192.168.1.123", "192.168.1.195", "192.168.1.202", "192.168.1.215"}
	now := time.Now().Unix()
	for i := 0; i < 120; i++ {
		l.record(ips[rand.Intn(len(ips))], domains[rand.Intn(len(domains))], now-int64(rand.Intn(3600)))
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
			l.record(ips[rand.Intn(len(ips))], domains[rand.Intn(len(domains))], time.Now().Unix())
		}
	}
}
