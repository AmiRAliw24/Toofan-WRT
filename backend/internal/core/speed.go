package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"separ/internal/env"
	"separ/internal/proxy"
)

// SpeedServer is a test destination: Cloudflare's speed endpoints or an
// Ookla (speedtest.net) server, many of which are inside Iran.
type SpeedServer struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"` // cloudflare | ookla
	Name        string  `json:"name"` // city
	Sponsor     string  `json:"sponsor"`
	Country     string  `json:"country"`
	CountryCode string  `json:"countryCode"`
	Host        string  `json:"host,omitempty"`
	URL         string  `json:"url,omitempty"`
	DistanceKm  float64 `json:"distanceKm,omitempty"`
}

var cloudflareServer = SpeedServer{ID: "cloudflare", Kind: "cloudflare", Name: "Anycast", Sponsor: "Cloudflare", Country: "Global"}

type SpeedResult struct {
	Running  bool        `json:"running"`
	Phase    string      `json:"phase"` // ping | download | upload | done | error
	Via      string      `json:"via"`   // tunnel | direct
	Server   SpeedServer `json:"server"`
	PingMs   int         `json:"pingMs"`
	JitterMs int         `json:"jitterMs"`
	DownMbps float64     `json:"downMbps"`
	UpMbps   float64     `json:"upMbps"`
	Error    string      `json:"error,omitempty"`
	At       int64       `json:"at"`
}

type SpeedTest struct {
	mu      sync.Mutex
	res     SpeedResult
	history []SpeedResult
	loaded  bool

	listMu sync.Mutex
	lists  map[string]serverList // by via+query
}

type serverList struct {
	at      time.Time
	servers []SpeedServer
}

const historyFile = "/etc/separ/speedtests.json"

func (s *SpeedTest) Get() SpeedResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.res
}

func (s *SpeedTest) History() []SpeedResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadHistory()
	out := make([]SpeedResult, len(s.history))
	copy(out, s.history)
	return out
}

func (s *SpeedTest) loadHistory() {
	if s.loaded {
		return
	}
	s.loaded = true
	if env.Mock {
		now := time.Now().Unix()
		s.history = []SpeedResult{
			{Phase: "done", Via: "tunnel", Server: cloudflareServer, PingMs: 142, JitterMs: 9, DownMbps: 34.2, UpMbps: 8.1, At: now - 3600},
			{Phase: "done", Via: "direct", Server: SpeedServer{Kind: "ookla", Name: "Tehran", Sponsor: "MCI", Country: "Iran", CountryCode: "IR"}, PingMs: 18, JitterMs: 3, DownMbps: 61.8, UpMbps: 12.4, At: now - 86400},
		}
		return
	}
	if b, err := os.ReadFile(historyFile); err == nil {
		json.Unmarshal(b, &s.history)
	}
}

func (s *SpeedTest) set(f func(r *SpeedResult)) {
	s.mu.Lock()
	f(&s.res)
	s.mu.Unlock()
}

// finish records a completed test in the history (newest first, max 30).
func (s *SpeedTest) finish(f func(r *SpeedResult)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.res)
	s.res.Running = false
	s.loadHistory()
	if s.res.Phase == "done" {
		s.history = append([]SpeedResult{s.res}, s.history...)
		if len(s.history) > 30 {
			s.history = s.history[:30]
		}
		if !env.Mock {
			if b, err := json.Marshal(s.history); err == nil {
				os.WriteFile(historyFile, b, 0o600)
			}
		}
	}
}

func speedClient(viaTunnel bool, timeout time.Duration) *http.Client {
	tr := &http.Transport{MaxIdleConnsPerHost: 8}
	if viaTunnel {
		pu, _ := url.Parse(fmt.Sprintf("socks5h://127.0.0.1:%d", proxy.SocksPort))
		tr.Proxy = http.ProxyURL(pu)
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

// Servers returns Cloudflare plus the nearest Ookla servers as seen from
// the chosen path (direct = Iranian line, tunnel = the server's country).
func (s *SpeedTest) Servers(viaTunnel bool, query string) ([]SpeedServer, error) {
	key := fmt.Sprintf("%v|%s", viaTunnel, strings.ToLower(query))
	s.listMu.Lock()
	if s.lists == nil {
		s.lists = map[string]serverList{}
	}
	if l, ok := s.lists[key]; ok && time.Since(l.at) < 30*time.Minute {
		s.listMu.Unlock()
		return l.servers, nil
	}
	s.listMu.Unlock()

	out := []SpeedServer{cloudflareServer}
	if env.Mock {
		out = append(out,
			SpeedServer{ID: "1", Kind: "ookla", Name: "Tehran", Sponsor: "MCI (Hamrah-e-Aval)", Country: "Iran", CountryCode: "IR", Host: "speedtest1.mci.ir:8080", DistanceKm: 3},
			SpeedServer{ID: "2", Kind: "ookla", Name: "Tehran", Sponsor: "Irancell", Country: "Iran", CountryCode: "IR", Host: "st1.irancell.ir:8080", DistanceKm: 5},
			SpeedServer{ID: "3", Kind: "ookla", Name: "Tehran", Sponsor: "Shatel", Country: "Iran", CountryCode: "IR", Host: "speedtest.shatel.ir:8080", DistanceKm: 6},
			SpeedServer{ID: "4", Kind: "ookla", Name: "Isfahan", Sponsor: "Mobinnet", Country: "Iran", CountryCode: "IR", Host: "sp1.mobinnet.ir:8080", DistanceKm: 340},
			SpeedServer{ID: "5", Kind: "ookla", Name: "Frankfurt", Sponsor: "Hetzner", Country: "Germany", CountryCode: "DE", Host: "speed.hetzner.de:8080", DistanceKm: 3800},
		)
		return out, nil
	}
	u := "https://www.speedtest.net/api/js/servers?engine=js&limit=25&https_functional=true"
	if query != "" {
		u += "&search=" + url.QueryEscape(query)
	}
	var raw []struct {
		ID       string  `json:"id"`
		URL      string  `json:"url"`
		Name     string  `json:"name"`
		Country  string  `json:"country"`
		CC       string  `json:"cc"`
		Sponsor  string  `json:"sponsor"`
		Host     string  `json:"host"`
		Distance float64 `json:"distance"`
	}
	resp, err := speedClient(viaTunnel, 15*time.Second).Get(u)
	if err != nil {
		return out, fmt.Errorf("server list: %v", err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		return out, fmt.Errorf("server list: %v", err)
	}
	for _, r := range raw {
		if r.Host == "" {
			continue
		}
		out = append(out, SpeedServer{ID: r.ID, Kind: "ookla", Name: r.Name, Sponsor: r.Sponsor, Country: r.Country,
			CountryCode: r.CC, Host: r.Host, URL: r.URL, DistanceKm: math.Round(r.Distance)})
	}
	s.listMu.Lock()
	s.lists[key] = serverList{at: time.Now(), servers: out}
	s.listMu.Unlock()
	return out, nil
}

// Start runs a test in the background.
func (s *SpeedTest) Start(viaTunnel bool, srv SpeedServer) bool {
	s.mu.Lock()
	if s.res.Running {
		s.mu.Unlock()
		return false
	}
	via := "direct"
	if viaTunnel {
		via = "tunnel"
	}
	if srv.Kind == "" {
		srv = cloudflareServer
	}
	s.res = SpeedResult{Running: true, Phase: "ping", Via: via, Server: srv, At: time.Now().Unix()}
	s.mu.Unlock()
	go s.run(viaTunnel, srv)
	return true
}

// endpoints of a destination: ping URL, download URL (per request) and upload URL.
type endpoints struct {
	ping       string
	legacyPing string
	download   func() string
	upload     func() string
	legacyDL   func() string
	legacyUL   func() string
}

func endpointsFor(srv SpeedServer) endpoints {
	if srv.Kind != "ookla" {
		const cf = "https://speed.cloudflare.com"
		return endpoints{
			ping:     cf + "/__down?bytes=0",
			download: func() string { return cf + "/__down?bytes=25000000" },
			upload:   func() string { return cf + "/__up" },
		}
	}
	base := "https://" + srv.Host
	nc := func() string { return fmt.Sprint(rand.Int63()) }
	e := endpoints{
		ping:     base + "/hello?nocache=" + nc(),
		download: func() string { return base + "/download?size=25000000&nocache=" + nc() },
		upload:   func() string { return base + "/upload?nocache=" + nc() },
	}
	// Older servers only have the classic upload.php / random*.jpg files.
	if srv.URL != "" {
		dir := srv.URL[:strings.LastIndex(srv.URL, "/")+1]
		if u, err := url.Parse(srv.URL); err == nil {
			dir = u.Scheme + "://" + u.Host + path.Dir(u.Path) + "/"
		}
		e.legacyPing = dir + "latency.txt"
		e.legacyDL = func() string { return dir + "random4000x4000.jpg?x=" + nc() }
		e.legacyUL = func() string { return srv.URL + "?x=" + nc() }
	}
	return e
}

func (s *SpeedTest) run(viaTunnel bool, srv SpeedServer) {
	if env.Mock {
		s.mockRun()
		return
	}
	cl := speedClient(viaTunnel, 30*time.Second)
	ep := endpointsFor(srv)
	fail := func(err error) {
		s.finish(func(r *SpeedResult) { r.Phase, r.Error = "error", err.Error() })
	}

	// Ping and jitter from 10 small requests on a warm connection.
	var pings []float64
	pingURL := ep.ping
	for i := 0; i < 11; i++ {
		start := time.Now()
		resp, err := cl.Get(pingURL)
		if err == nil && resp.StatusCode >= 400 {
			resp.Body.Close()
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		if err != nil {
			if i == 0 && ep.legacyPing != "" && pingURL != ep.legacyPing {
				pingURL = ep.legacyPing
				i--
				continue
			}
			if i == 0 {
				fail(fmt.Errorf("server unreachable: %v", err))
				return
			}
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if i > 0 { // the first one includes the TLS handshake
			pings = append(pings, float64(time.Since(start).Microseconds())/1000)
		}
	}
	if len(pings) == 0 {
		fail(errors.New("no ping replies"))
		return
	}
	ping, jitter := pingStats(pings)
	s.set(func(r *SpeedResult) { r.PingMs, r.JitterMs, r.Phase = int(ping), int(jitter), "download" })

	dlURL := ep.download
	if ep.legacyDL != nil && !reachable(cl, ep.download()) {
		dlURL = ep.legacyDL
	}
	down := measure(12*time.Second, 4, func(stop <-chan struct{}, add func(int)) {
		for {
			select {
			case <-stop:
				return
			default:
			}
			resp, err := cl.Get(dlURL())
			if err != nil {
				time.Sleep(200 * time.Millisecond)
				continue
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

	ulURL := ep.upload
	up := measure(10*time.Second, 3, func(stop <-chan struct{}, add func(int)) {
		chunk := strings.Repeat("0", 2<<20)
		for {
			select {
			case <-stop:
				return
			default:
			}
			body := &countingReader{r: strings.NewReader(chunk), add: add, stop: stop}
			resp, err := cl.Post(ulURL(), "application/octet-stream", body)
			if err != nil {
				if ep.legacyUL != nil {
					ulURL = ep.legacyUL
				}
				time.Sleep(200 * time.Millisecond)
				continue
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}, func(mbps float64) { s.set(func(r *SpeedResult) { r.UpMbps = mbps }) })
	s.finish(func(r *SpeedResult) { r.UpMbps = up; r.Phase = "done" })
}

func reachable(cl *http.Client, u string) bool {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Range", "bytes=0-1023")
	resp, err := cl.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 400
}

// pingStats returns the median latency and the mean difference between
// consecutive samples (jitter).
func pingStats(p []float64) (median, jitter float64) {
	sorted := append([]float64{}, p...)
	sort.Float64s(sorted)
	median = sorted[len(sorted)/2]
	if len(p) > 1 {
		var sum float64
		for i := 1; i < len(p); i++ {
			sum += math.Abs(p[i] - p[i-1])
		}
		jitter = sum / float64(len(p)-1)
	}
	return math.Round(median), math.Round(jitter)
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
	// Skip the TCP slow-start seconds when computing the final number.
	var warmBytes int
	var warmAt time.Time
	tk := time.NewTicker(500 * time.Millisecond)
	for time.Since(start) < d {
		<-tk.C
		mu.Lock()
		t := total
		mu.Unlock()
		if warmAt.IsZero() && time.Since(start) >= 2*time.Second {
			warmBytes, warmAt = t, time.Now()
		}
		if !warmAt.IsZero() && time.Since(warmAt) > time.Second {
			live(mbps(t-warmBytes, time.Since(warmAt)))
		} else {
			live(mbps(t, time.Since(start)))
		}
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
	time.Sleep(1200 * time.Millisecond)
	s.set(func(r *SpeedResult) { r.PingMs, r.JitterMs = 140+rand.Intn(40), 4+rand.Intn(10); r.Phase = "download" })
	for i := 0; i < 16; i++ {
		time.Sleep(450 * time.Millisecond)
		s.set(func(r *SpeedResult) { r.DownMbps = float64(int((28+rand.Float64()*12)*10)) / 10 })
	}
	s.set(func(r *SpeedResult) { r.Phase = "upload" })
	for i := 0; i < 12; i++ {
		time.Sleep(450 * time.Millisecond)
		s.set(func(r *SpeedResult) { r.UpMbps = float64(int((6+rand.Float64()*4)*10)) / 10 })
	}
	s.finish(func(r *SpeedResult) { r.Phase = "done" })
}
