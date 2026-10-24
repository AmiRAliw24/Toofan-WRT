package core

import (
	"encoding/json"
	"math/rand"
	"net"
	"os"
	"sync"
	"time"

	"separ/internal/env"
	"separ/internal/proxy"
)

// Sample is one minute of line-quality measurements; -1 means failed.
type Sample struct {
	T   int64 `json:"t"`
	Ext int   `json:"ext"` // international latency (through the tunnel when up)
	Dom int   `json:"dom"` // domestic latency
}

type Quality struct {
	c       *Core
	mu      sync.RWMutex
	samples []Sample
	path    string
}

const maxSamples = 7 * 24 * 60

var domesticTargets = []string{"www.aparat.com:443", "www.digikala.com:443", "divar.ir:443"}
var foreignTargets = []string{"1.1.1.1:443", "8.8.8.8:443"}

func NewQuality(c *Core) *Quality {
	q := &Quality{c: c, path: "/etc/separ/quality.json"}
	if env.Mock {
		q.seedMock()
	} else if b, err := os.ReadFile(q.path); err == nil {
		json.Unmarshal(b, &q.samples)
	}
	return q
}

func (q *Quality) Run() {
	t := time.NewTicker(time.Minute)
	saveEvery := 0
	for {
		<-t.C
		s := q.measure()
		q.c.Incidents.Observe("line", s.Dom >= 0 || s.Ext >= 0)
		if s.Dom >= 0 {
			q.c.Incidents.Observe("international", s.Ext >= 0)
		}
		if q.c.Store.Get().Proxy.Enabled {
			m := q.c.Proxy.Mode()
			q.c.Incidents.Observe("tunnel", m == proxy.ModeConnected || m == proxy.ModeConnecting)
		}
		q.mu.Lock()
		q.samples = append(q.samples, s)
		if len(q.samples) > maxSamples {
			q.samples = q.samples[len(q.samples)-maxSamples:]
		}
		q.mu.Unlock()
		saveEvery++
		if saveEvery%15 == 0 && !env.Mock {
			q.save()
		}
	}
}

func (q *Quality) save() {
	q.mu.RLock()
	b, _ := json.Marshal(q.samples)
	q.mu.RUnlock()
	os.MkdirAll("/etc/separ", 0o700)
	os.WriteFile(q.path+".tmp", b, 0o600)
	os.Rename(q.path+".tmp", q.path)
}

func (q *Quality) measure() Sample {
	s := Sample{T: time.Now().Unix(), Ext: -1, Dom: -1}
	if env.Mock {
		s.Ext = 120 + rand.Intn(80)
		s.Dom = 60 + rand.Intn(60)
		return s
	}
	s.Dom = bestConnect(domesticTargets)
	if q.c.Proxy.Mode() == proxy.ModeConnected {
		if d, err := proxy.ProbeSocks(proxy.SocksPort, 8*time.Second); err == nil {
			s.Ext = int(d.Milliseconds())
		}
	} else {
		s.Ext = bestConnect(foreignTargets)
	}
	return s
}

func bestConnect(targets []string) int {
	best := -1
	for _, t := range targets {
		start := time.Now()
		c, err := net.DialTimeout("tcp", t, 4*time.Second)
		if err != nil {
			continue
		}
		c.Close()
		ms := int(time.Since(start).Milliseconds())
		if best < 0 || ms < best {
			best = ms
		}
	}
	return best
}

// Last returns the most recent sample.
func (q *Quality) Last() Sample {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if len(q.samples) == 0 {
		return Sample{Ext: -1, Dom: -1}
	}
	return q.samples[len(q.samples)-1]
}

type QualityReport struct {
	Samples    []Sample `json:"samples"`
	Stability  float64  `json:"stability"`  // % of minutes where both worked
	DomesticUp float64  `json:"domesticUp"` // % reachability
	ForeignUp  float64  `json:"foreignUp"`
	AvgExt     float64  `json:"avgExt"`
	AvgDom     float64  `json:"avgDom"`
	LastExt    int      `json:"lastExt"`
	LastDom    int      `json:"lastDom"`
}

// Report summarises a window, downsampled to at most ~240 points.
func (q *Quality) Report(window time.Duration) QualityReport {
	q.mu.RLock()
	defer q.mu.RUnlock()
	from := time.Now().Add(-window).Unix()
	var in []Sample
	for _, s := range q.samples {
		if s.T >= from {
			in = append(in, s)
		}
	}
	r := QualityReport{LastExt: -1, LastDom: -1}
	if len(in) == 0 {
		return r
	}
	var okBoth, okDom, okExt, sumExt, sumDom float64
	for _, s := range in {
		if s.Dom >= 0 {
			okDom++
			sumDom += float64(s.Dom)
		}
		if s.Ext >= 0 {
			okExt++
			sumExt += float64(s.Ext)
		}
		if s.Dom >= 0 && s.Ext >= 0 {
			okBoth++
		}
	}
	n := float64(len(in))
	r.Stability, r.DomesticUp, r.ForeignUp = round1(okBoth/n*100), round1(okDom/n*100), round1(okExt/n*100)
	if okExt > 0 {
		r.AvgExt = round1(sumExt / okExt)
	}
	if okDom > 0 {
		r.AvgDom = round1(sumDom / okDom)
	}
	r.LastExt, r.LastDom = in[len(in)-1].Ext, in[len(in)-1].Dom
	step := len(in)/240 + 1
	for i := 0; i < len(in); i += step {
		// Keep the worst value of each bucket so spikes stay visible.
		b := in[i]
		for j := i + 1; j < i+step && j < len(in); j++ {
			if in[j].Ext > b.Ext || in[j].Ext < 0 {
				b.Ext = in[j].Ext
			}
			if in[j].Dom > b.Dom || in[j].Dom < 0 {
				b.Dom = in[j].Dom
			}
		}
		r.Samples = append(r.Samples, b)
	}
	return r
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

func (q *Quality) seedMock() {
	now := time.Now().Unix()
	for i := maxSamples; i > 0; i-- {
		s := Sample{T: now - int64(i*60), Ext: 105 + rand.Intn(60), Dom: 55 + rand.Intn(40)}
		if rand.Intn(40) == 0 {
			s.Ext += 150
		}
		if rand.Intn(25) == 0 {
			s.Dom += 120
		}
		q.samples = append(q.samples, s)
	}
}
