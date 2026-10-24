package gaming

import (
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"toofanwrt/internal/env"
)

// BloatResult compares latency at rest with latency while the line is
// saturated by a download.
type BloatResult struct {
	Running  bool    `json:"running"`
	Phase    string  `json:"phase"` // idle | load | done | error
	IdleMs   float64 `json:"idleMs"`
	LoadedMs float64 `json:"loadedMs"`
	Grade    string  `json:"grade"`
	DownMbps float64 `json:"downMbps"`
	Error    string  `json:"error,omitempty"`
	At       int64   `json:"at"`
}

type Bloat struct {
	mu  sync.Mutex
	res BloatResult
}

func (b *Bloat) Get() BloatResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.res
}

func (b *Bloat) set(f func(r *BloatResult)) {
	b.mu.Lock()
	f(&b.res)
	b.mu.Unlock()
}

// Domestic hosts measure the line itself, independent of any tunnel.
var latencyTargets = []string{"www.aparat.com:443", "www.digikala.com:443", "1.1.1.1:443"}

// Start runs the test; socksPort > 0 sends the load through the tunnel.
func (b *Bloat) Start(socksPort int) bool {
	b.mu.Lock()
	if b.res.Running {
		b.mu.Unlock()
		return false
	}
	b.res = BloatResult{Running: true, Phase: "idle", At: time.Now().Unix()}
	b.mu.Unlock()
	go b.run(socksPort)
	return true
}

func grade(delta float64) string {
	switch {
	case delta < 5:
		return "A+"
	case delta < 30:
		return "A"
	case delta < 60:
		return "B"
	case delta < 200:
		return "C"
	case delta < 400:
		return "D"
	}
	return "F"
}

func (b *Bloat) run(socksPort int) {
	if env.Mock {
		time.Sleep(2 * time.Second)
		b.set(func(r *BloatResult) { r.Phase, r.IdleMs = "load", 62 })
		time.Sleep(6 * time.Second)
		b.set(func(r *BloatResult) {
			r.LoadedMs, r.DownMbps = 62+float64(20+rand.Intn(120)), 31.4
			r.Grade, r.Phase, r.Running = grade(r.LoadedMs-r.IdleMs), "done", false
		})
		return
	}
	target := ""
	for _, t := range latencyTargets {
		if _, err := connectRTT(t); err == nil {
			target = t
			break
		}
	}
	if target == "" {
		b.set(func(r *BloatResult) { r.Running, r.Phase, r.Error = false, "error", "no latency target reachable" })
		return
	}
	idle := sampleRTT(target, 10, nil)
	b.set(func(r *BloatResult) { r.IdleMs, r.Phase = idle, "load" })

	tr := &http.Transport{}
	if socksPort > 0 {
		pu, _ := url.Parse(fmt.Sprintf("socks5h://127.0.0.1:%d", socksPort))
		tr.Proxy = http.ProxyURL(pu)
	}
	cl := &http.Client{Transport: tr, Timeout: 20 * time.Second}
	stop := make(chan struct{})
	var mu sync.Mutex
	total := 0
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				resp, err := cl.Get("https://speed.cloudflare.com/__down?bytes=50000000")
				if err != nil {
					time.Sleep(300 * time.Millisecond)
					continue
				}
				buf := make([]byte, 32*1024)
				for {
					n, err := resp.Body.Read(buf)
					mu.Lock()
					total += n
					mu.Unlock()
					if err != nil {
						break
					}
					select {
					case <-stop:
						resp.Body.Close()
						return
					default:
					}
				}
				resp.Body.Close()
			}
		}()
	}
	start := time.Now()
	time.Sleep(2 * time.Second) // let the queue fill
	loaded := sampleRTT(target, 20, stop)
	close(stop)
	mu.Lock()
	mbps := float64(total) * 8 / time.Since(start).Seconds() / 1e6
	mu.Unlock()
	go wg.Wait()
	b.set(func(r *BloatResult) {
		r.LoadedMs, r.DownMbps = loaded, float64(int(mbps*10))/10
		r.Grade, r.Phase, r.Running = grade(loaded-r.IdleMs), "done", false
	})
}

// sampleRTT returns the median TCP handshake time over n samples.
func sampleRTT(target string, n int, stop chan struct{}) float64 {
	var v []float64
	for i := 0; i < n; i++ {
		if d, err := connectRTT(target); err == nil {
			v = append(v, float64(d.Microseconds())/1000)
		}
		time.Sleep(250 * time.Millisecond)
	}
	if len(v) == 0 {
		return -1
	}
	sort.Float64s(v)
	m := v[len(v)/2]
	return float64(int(m*10)) / 10
}

func connectRTT(target string) (time.Duration, error) {
	start := time.Now()
	c, err := net.DialTimeout("tcp", target, 3*time.Second)
	if err != nil {
		return 0, err
	}
	d := time.Since(start)
	c.Close()
	return d, nil
}
