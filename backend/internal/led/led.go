// Package led drives an RGB status LED (Google WiFi's light ring) through
// /sys/class/leds, showing network state without opening the panel.
package led

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"separ/internal/config"
	"separ/internal/env"
)

// Status is what the LED reflects in automatic modes.
type Status struct {
	WANUp      bool
	ProxyMode  string // proxy.Mode* values
	Split      bool
	ExtPingMs  int // -1 unknown/failed
	SpeedTest  bool
	SpeedGrade string  // ok | mid | bad, shown briefly after a test
	SpeedMbps  float64 // estimated line speed, -1 unknown
}

type RGB struct{ R, G, B float64 } // 0..1

type channel struct {
	path string
	max  float64
	last int
}

type Controller struct {
	Config func() config.LED
	Status func() Status

	mu        sync.Mutex
	red       *channel
	green     *channel
	blue      *channel
	white     *channel
	origTrig  map[string]string
	owned     bool
	gradeTill time.Time
	grade     string
	wasTest   bool
	current   RGB // last colour shown, for the UI preview
}

func New() *Controller {
	c := &Controller{origTrig: map[string]string{}}
	c.discover()
	return c
}

// Supported reports whether an RGB LED was found.
func (c *Controller) Supported() bool {
	if env.Mock {
		return true
	}
	return c.red != nil && c.green != nil && c.blue != nil
}

// Current returns the colour being shown (hex) for the UI.
func (c *Controller) Current() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return hex(c.current)
}

func (c *Controller) discover() {
	dirs, _ := filepath.Glob("/sys/class/leds/*")
	for _, d := range dirs {
		name := strings.ToLower(filepath.Base(d))
		mb := readFloat(filepath.Join(d, "max_brightness"))
		if mb <= 0 {
			mb = 255
		}
		ch := &channel{path: d, max: mb, last: -1}
		switch {
		case strings.Contains(name, "red") && c.red == nil:
			c.red = ch
		case strings.Contains(name, "green") && c.green == nil:
			c.green = ch
		case strings.Contains(name, "blue") && c.blue == nil:
			c.blue = ch
		case strings.Contains(name, "white") && c.white == nil:
			c.white = ch
		}
	}
}

func (c *Controller) channels() []*channel {
	var out []*channel
	for _, ch := range []*channel{c.red, c.green, c.blue, c.white} {
		if ch != nil {
			out = append(out, ch)
		}
	}
	return out
}

// takeOver switches the LEDs to manual control, remembering OpenWrt's triggers.
func (c *Controller) takeOver() {
	if c.owned {
		return
	}
	for _, ch := range c.channels() {
		trig := readTrigger(ch.path)
		c.origTrig[ch.path] = trig
		os.WriteFile(filepath.Join(ch.path, "trigger"), []byte("none"), 0o644)
		ch.last = -1
	}
	c.owned = true
}

// release hands the LEDs back to OpenWrt's own configuration.
func (c *Controller) release() {
	if !c.owned {
		return
	}
	for _, ch := range c.channels() {
		if t := c.origTrig[ch.path]; t != "" {
			os.WriteFile(filepath.Join(ch.path, "trigger"), []byte(t), 0o644)
		}
	}
	c.owned = false
	env.Run(10*time.Second, "/etc/init.d/led", "restart")
}

func (c *Controller) Run() {
	if !c.Supported() {
		return
	}
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for now := range t.C {
		c.frame(now)
	}
}

var (
	colPurple = RGB{0.55, 0.20, 1.0}
	colGreen  = RGB{0, 1, 0.25}
	colCyan   = RGB{0, 0.85, 0.9}
	colYellow = RGB{1, 0.6, 0}
	colRed    = RGB{1, 0, 0}
	colWhite  = RGB{1, 1, 1}
)

func (c *Controller) frame(now time.Time) {
	cfg := c.Config()
	if cfg.Mode == "default" {
		c.mu.Lock()
		c.release()
		c.mu.Unlock()
		return
	}
	st := c.Status()
	col, pulse := c.pick(cfg, st, now)
	level := float64(cfg.Brightness) / 100
	if level <= 0 {
		level = 0.6
	}
	if pulse > 0 {
		// Smooth breathing between 25% and 100%.
		phase := float64(now.UnixMilli()%int64(pulse)) / float64(pulse)
		level *= 0.25 + 0.75*(0.5-0.5*math.Cos(2*math.Pi*phase))
	}
	if cfg.NightOff && inWindow(now, cfg.NightFrom, cfg.NightTo) && !st.SpeedTest {
		level = 0
	}
	if cfg.Mode == "off" {
		level = 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current = RGB{col.R * level, col.G * level, col.B * level}
	if env.Mock {
		return
	}
	c.takeOver()
	c.write(c.red, col.R*level)
	c.write(c.green, col.G*level)
	c.write(c.blue, col.B*level)
	c.write(c.white, 0)
}

// pick returns the colour and pulse period in ms (0 = steady).
func (c *Controller) pick(cfg config.LED, st Status, now time.Time) (RGB, int) {
	// Speed test: fast pulse while running, then the result for 10 s.
	if st.SpeedTest {
		c.wasTest = true
		return colWhite, 600
	}
	if c.wasTest {
		c.wasTest = false
		c.grade, c.gradeTill = st.SpeedGrade, now.Add(10*time.Second)
	}
	if now.Before(c.gradeTill) {
		switch c.grade {
		case "ok":
			return colGreen, 0
		case "mid":
			return colYellow, 0
		default:
			return colRed, 0
		}
	}
	switch cfg.Mode {
	case "color":
		return parseHex(cfg.Color), 0
	case "ping":
		switch {
		case !st.WANUp || st.ExtPingMs < 0:
			return colRed, 1500
		case st.ExtPingMs < 50:
			return colGreen, 0
		case st.ExtPingMs < 120:
			return colCyan, 0
		case st.ExtPingMs < 250:
			return colYellow, 0
		default:
			return colRed, 0
		}
	case "speed":
		good, mid := float64(cfg.SpeedGood), float64(cfg.SpeedMid)
		if good <= 0 {
			good = 20
		}
		if mid <= 0 || mid >= good {
			mid = good / 4
		}
		switch {
		case !st.WANUp:
			return colRed, 1500
		case st.SpeedMbps < 0:
			return colCyan, 2500 // unknown until the first test or heavy use
		case st.SpeedMbps >= good:
			return colGreen, 0
		case st.SpeedMbps >= mid:
			return colYellow, 0
		default:
			return colRed, 0
		}
	}
	// auto
	if !st.WANUp {
		return colRed, 0
	}
	switch st.ProxyMode {
	case "connected":
		if st.Split {
			return colPurple, 0
		}
		return colGreen, 0
	case "off":
		return colCyan, 0
	case "killswitch", "safe":
		return colRed, 1500
	default: // connecting, degraded, fallback
		return colYellow, 2000
	}
}

func (c *Controller) write(ch *channel, v float64) {
	if ch == nil {
		return
	}
	n := int(math.Round(math.Max(0, math.Min(1, v)) * ch.max))
	if n == ch.last {
		return
	}
	ch.last = n
	os.WriteFile(filepath.Join(ch.path, "brightness"), []byte(strconv.Itoa(n)), 0o644)
}

func inWindow(now time.Time, from, to string) bool {
	f, ok1 := minutes(from)
	t, ok2 := minutes(to)
	if !ok1 || !ok2 || f == t {
		return false
	}
	m := now.Hour()*60 + now.Minute()
	if f < t {
		return m >= f && m < t
	}
	return m >= f || m < t
}

func minutes(s string) (int, bool) {
	h, m, ok := strings.Cut(s, ":")
	if !ok {
		return 0, false
	}
	hh, e1 := strconv.Atoi(h)
	mm, e2 := strconv.Atoi(m)
	if e1 != nil || e2 != nil {
		return 0, false
	}
	return hh*60 + mm, true
}

func parseHex(s string) RGB {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return colGreen
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return colGreen
	}
	return RGB{float64(v>>16&0xff) / 255, float64(v>>8&0xff) / 255, float64(v&0xff) / 255}
}

func hex(c RGB) string {
	b := func(f float64) int { return int(math.Round(math.Max(0, math.Min(1, f)) * 255)) }
	return "#" + strconv.FormatInt(int64(1<<24|b(c.R)<<16|b(c.G)<<8|b(c.B)), 16)[1:]
}

func readFloat(p string) float64 {
	b, err := os.ReadFile(p)
	if err != nil {
		return 0
	}
	f, _ := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	return f
}

// readTrigger extracts the active trigger, shown in [brackets] by the kernel.
func readTrigger(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "trigger"))
	if err != nil {
		return ""
	}
	s := string(b)
	i, j := strings.Index(s, "["), strings.Index(s, "]")
	if i >= 0 && j > i {
		return s[i+1 : j]
	}
	return ""
}
