package devices

import (
	"encoding/json"
	"math/rand"
	"os"
	"sort"
	"sync"
	"time"

	"toofanwrt/internal/env"
)

// usage keeps per-device traffic per day for two months, so the panel can
// show today, this month and a daily chart across reboots.
type usage struct {
	mu    sync.Mutex
	days  map[string]map[string]*[2]uint64 // mac -> YYYY-MM-DD -> [down, up]
	dirty bool
	saved time.Time
}

const usageFile = "/etc/toofanwrt/usage.json"

type DayUsage struct {
	Day  string `json:"day"`
	Down uint64 `json:"down"`
	Up   uint64 `json:"up"`
}

func newUsage() *usage {
	u := &usage{days: map[string]map[string]*[2]uint64{}, saved: time.Now()}
	if env.Mock {
		return u
	}
	if b, err := os.ReadFile(usageFile); err == nil {
		json.Unmarshal(b, &u.days)
	}
	return u
}

func (u *usage) add(mac string, down, up uint64, now time.Time) {
	if down == 0 && up == 0 {
		return
	}
	day := now.Format("2006-01-02")
	u.mu.Lock()
	defer u.mu.Unlock()
	m := u.days[mac]
	if m == nil {
		m = map[string]*[2]uint64{}
		u.days[mac] = m
	}
	v := m[day]
	if v == nil {
		v = &[2]uint64{}
		m[day] = v
	}
	v[0] += down
	v[1] += up
	u.dirty = true
	// Flash-friendly: write at most every 10 minutes.
	if time.Since(u.saved) > 10*time.Minute {
		u.saveLocked(now)
	}
}

func (u *usage) saveLocked(now time.Time) {
	u.saved = now
	if !u.dirty || env.Mock {
		return
	}
	cut := now.AddDate(0, 0, -62).Format("2006-01-02")
	for mac, m := range u.days {
		for d := range m {
			if d < cut {
				delete(m, d)
			}
		}
		if len(m) == 0 {
			delete(u.days, mac)
		}
	}
	if b, err := json.Marshal(u.days); err == nil {
		tmp := usageFile + ".tmp"
		if os.WriteFile(tmp, b, 0o600) == nil && os.Rename(tmp, usageFile) == nil {
			u.dirty = false
		}
	}
}

// Save flushes pending counts (on shutdown).
func (t *Tracker) SaveUsage() {
	t.usage.mu.Lock()
	t.usage.saveLocked(time.Now())
	t.usage.mu.Unlock()
}

// Usage returns a device's daily traffic for the last n days, oldest first.
func (t *Tracker) Usage(mac string, n int) []DayUsage {
	now := time.Now()
	t.usage.mu.Lock()
	defer t.usage.mu.Unlock()
	if env.Mock && len(t.usage.days[mac]) < 3 {
		m := map[string]*[2]uint64{}
		for i := 0; i < 40; i++ {
			d := now.AddDate(0, 0, -i).Format("2006-01-02")
			m[d] = &[2]uint64{uint64(rand.Int63n(3e9)), uint64(rand.Int63n(4e8))}
		}
		t.usage.days[mac] = m
	}
	out := make([]DayUsage, 0, n)
	for i := n - 1; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		du := DayUsage{Day: d}
		if v := t.usage.days[mac][d]; v != nil {
			du.Down, du.Up = v[0], v[1]
		}
		out = append(out, du)
	}
	return out
}

// MonthUsage sums the current calendar month.
func (t *Tracker) MonthUsage(mac string) (down, up uint64) {
	prefix := time.Now().Format("2006-01-")
	t.usage.mu.Lock()
	defer t.usage.mu.Unlock()
	days := make([]string, 0)
	for d := range t.usage.days[mac] {
		days = append(days, d)
	}
	sort.Strings(days)
	for _, d := range days {
		if len(d) >= 8 && d[:8] == prefix {
			v := t.usage.days[mac][d]
			down += v[0]
			up += v[1]
		}
	}
	return
}
