package core

import (
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"separ/internal/env"
	"separ/internal/proxy"
)

type SpeedResult struct {
	Running  bool    `json:"running"`
	Phase    string  `json:"phase"` // ping | download | upload | done | error
	Via      string  `json:"via"`   // tunnel | direct
	PingMs   int     `json:"pingMs"`
	DownMbps float64 `json:"downMbps"`
	UpMbps   float64 `json:"upMbps"`
	Error    string  `json:"error,omitempty"`
	At       int64   `json:"at"`
}

type SpeedTest struct {
	mu  sync.Mutex
	res SpeedResult
}

func (s *SpeedTest) Get() SpeedResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.res
}

func (s *SpeedTest) set(f func(r *SpeedResult)) {
	s.mu.Lock()
	f(&s.res)
	s.mu.Unlock()
}

// Start runs a test in the background; through the tunnel when it's up.
func (s *SpeedTest) Start(viaTunnel bool) bool {
	s.mu.Lock()
	if s.res.Running {
		s.mu.Unlock()
		return false
	}
	via := "direct"
	if viaTunnel {
		via = "tunnel"
	}
	s.res = SpeedResult{Running: true, Phase: "ping", Via: via, At: time.Now().Unix()}
	s.mu.Unlock()
	go s.run(viaTunnel)
	return true
}

const speedHost = "https://speed.cloudflare.com"

func (s *SpeedTest) run(viaTunnel bool) {
	if env.Mock {
		s.mockRun()
		return
	}
	tr := &http.Transport{}
	if viaTunnel {
		pu, _ := url.Parse(fmt.Sprintf("socks5h://127.0.0.1:%d", proxy.SocksPort))
		tr.Proxy = http.ProxyURL(pu)
	}
	cl := &http.Client{Transport: tr, Timeout: 30 * time.Second}
	fail := func(err error) {
		s.set(func(r *SpeedResult) { r.Running, r.Phase, r.Error = false, "error", err.Error() })
	}

	// Ping: best of a few tiny requests on a warm connection.
	best := time.Duration(0)
	for i := 0; i < 4; i++ {
		start := time.Now()
		resp, err := cl.Get(speedHost + "/__down?bytes=0")
		if err != nil {
			if i == 0 {
				fail(err)
				return
			}
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if d := time.Since(start); i > 0 && (best == 0 || d < best) {
			best = d
		}
	}
	s.set(func(r *SpeedResult) { r.PingMs = int(best.Milliseconds()); r.Phase = "download" })

	// Download: parallel streams for ~10s.
	down := measure(10*time.Second, 4, func(stop <-chan struct{}, add func(int)) {
		for {
			select {
			case <-stop:
				return
			default:
			}
			resp, err := cl.Get(speedHost + "/__down?bytes=25000000")
			if err != nil {
				return
			}
			buf := make([]byte, 32*1024)
			for {
				n, err := resp.Body.Read(buf)
				add(n)
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
	}, func(mbps float64) { s.set(func(r *SpeedResult) { r.DownMbps = mbps }) })
	s.set(func(r *SpeedResult) { r.DownMbps = down; r.Phase = "upload" })

	up := measure(8*time.Second, 2, func(stop <-chan struct{}, add func(int)) {
		chunk := strings.Repeat("0", 1<<20)
		for {
			select {
			case <-stop:
				return
			default:
			}
			body := &countingReader{r: strings.NewReader(chunk), add: add, stop: stop}
			resp, err := cl.Post(speedHost+"/__up", "application/octet-stream", body)
			if err != nil {
				return
			}
			resp.Body.Close()
		}
	}, func(mbps float64) { s.set(func(r *SpeedResult) { r.UpMbps = mbps }) })
	s.set(func(r *SpeedResult) { r.UpMbps = up; r.Phase = "done"; r.Running = false })
}

type countingReader struct {
	r    io.Reader
	add  func(int)
	stop <-chan struct{}
}

func (c *countingReader) Read(p []byte) (int, error) {
	select {
	case <-c.stop:
		return 0, io.EOF
	default:
	}
	if len(p) > 32*1024 {
		p = p[:32*1024]
	}
	n, err := c.r.Read(p)
	c.add(n)
	return n, err
}

// measure runs workers for d and reports Mbps, updating live every 500ms.
func measure(d time.Duration, workers int, work func(stop <-chan struct{}, add func(int)), live func(float64)) float64 {
	var mu sync.Mutex
	total := 0
	add := func(n int) { mu.Lock(); total += n; mu.Unlock() }
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); work(stop, add) }()
	}
	start := time.Now()
	// Skip the TCP slow-start second when computing the final number.
	var warmBytes int
	var warmAt time.Time
	tk := time.NewTicker(500 * time.Millisecond)
	for time.Since(start) < d {
		<-tk.C
		mu.Lock()
		t := total
		mu.Unlock()
		if warmAt.IsZero() && time.Since(start) >= 1500*time.Millisecond {
			warmBytes, warmAt = t, time.Now()
		}
		live(mbps(t, time.Since(start)))
	}
	tk.Stop()
	close(stop)
	mu.Lock()
	t := total
	mu.Unlock()
	go wg.Wait()
	if !warmAt.IsZero() && t > warmBytes {
		return mbps(t-warmBytes, time.Since(warmAt))
	}
	return mbps(t, time.Since(start))
}

func mbps(bytes int, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	v := float64(bytes) * 8 / d.Seconds() / 1e6
	return float64(int(v*10+0.5)) / 10
}

func (s *SpeedTest) mockRun() {
	time.Sleep(time.Second)
	s.set(func(r *SpeedResult) { r.PingMs = 140 + rand.Intn(40); r.Phase = "download" })
	for i := 0; i < 16; i++ {
		time.Sleep(500 * time.Millisecond)
		s.set(func(r *SpeedResult) { r.DownMbps = float64(int((28+rand.Float64()*12)*10)) / 10 })
	}
	s.set(func(r *SpeedResult) { r.Phase = "upload" })
	for i := 0; i < 12; i++ {
		time.Sleep(500 * time.Millisecond)
		s.set(func(r *SpeedResult) { r.UpMbps = float64(int((6+rand.Float64()*4)*10)) / 10 })
	}
	s.set(func(r *SpeedResult) { r.Phase = "done"; r.Running = false })
}
