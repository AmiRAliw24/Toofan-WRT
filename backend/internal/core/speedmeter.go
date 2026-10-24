package core

import (
	"sync"
	"time"
)

// speedMeter estimates how fast the line is right now, for the LED's speed
// mode: the last speed test, raised by the highest download rate actually
// reached in the past 10 minutes (heavy use is a free measurement).
type speedMeter struct {
	mu    sync.Mutex
	peaks []rateSample // one per 2 s
	est   float64      // cached estimate (the LED reads it 20 times a second)
	src   string
}

type rateSample struct {
	at   time.Time
	mbps float64
}

const speedWindow = 10 * time.Minute

func (m *speedMeter) add(mbps float64, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.peaks = append(m.peaks, rateSample{now, mbps})
	cut := 0
	for cut < len(m.peaks) && now.Sub(m.peaks[cut].at) > speedWindow {
		cut++
	}
	m.peaks = m.peaks[cut:]
}

func (m *speedMeter) peak() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	best := 0.0
	for _, s := range m.peaks {
		best = max(best, s.mbps)
	}
	return best
}

func (c *Core) speedLoop() {
	for {
		c.meter.add(c.Sampler.WAN().RxBps*8/1e6, time.Now())
		est, src := c.estimateSpeed()
		c.meter.mu.Lock()
		c.meter.est, c.meter.src = est, src
		c.meter.mu.Unlock()
		time.Sleep(2 * time.Second)
	}
}

// SpeedEstimate returns the line speed in Mbps and where it came from
// (test | traffic), or -1 when nothing is known yet.
func (c *Core) SpeedEstimate() (float64, string) {
	c.meter.mu.Lock()
	defer c.meter.mu.Unlock()
	if c.meter.src == "" {
		return -1, ""
	}
	return c.meter.est, c.meter.src
}

func (c *Core) estimateSpeed() (float64, string) {
	test := -1.0
	if h := c.Speed.History(); len(h) > 0 {
		test = h[0].DownMbps
	}
	if r := c.Speed.Get(); r.Phase == "done" && r.DownMbps > 0 {
		test = r.DownMbps
	}
	if p := c.meter.peak(); p > test && p >= 1 {
		return p, "traffic"
	}
	if test < 0 {
		return -1, ""
	}
	return test, "test"
}
