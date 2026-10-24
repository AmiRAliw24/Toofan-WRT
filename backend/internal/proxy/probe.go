package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"separ/internal/config"
	"separ/internal/env"
)

var probeURLs = []string{
	"https://www.gstatic.com/generate_204",
	"https://cp.cloudflare.com/generate_204",
}

// ProbeSocks sends real HTTPS traffic through a local SOCKS port. It returns
// the round-trip of a request on an already-open tunnel connection, which is
// what users feel as "ping" while browsing.
func ProbeSocks(port int, timeout time.Duration) (time.Duration, error) {
	pu, _ := url.Parse(fmt.Sprintf("socks5h://127.0.0.1:%d", port))
	tr := &http.Transport{Proxy: http.ProxyURL(pu), DisableKeepAlives: false, MaxIdleConns: 1}
	defer tr.CloseIdleConnections()
	cl := &http.Client{Transport: tr, Timeout: timeout}
	var lastErr error
	for _, u := range probeURLs {
		if _, err := get(cl, u); err != nil {
			lastErr = err
			continue
		}
		d, err := get(cl, u)
		if err != nil {
			lastErr = err
			continue
		}
		return d, nil
	}
	return 0, lastErr
}

func get(cl *http.Client, u string) (time.Duration, error) {
	start := time.Now()
	resp, err := cl.Get(u)
	if err != nil {
		return 0, simplifyErr(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return time.Since(start), nil
}

func simplifyErr(err error) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return errors.New("timeout")
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

type TestResult struct {
	ID        string `json:"id"`
	LatencyMs int    `json:"latencyMs"` // -1 failed
	Error     string `json:"error,omitempty"`
}

// Tester measures many servers at once with one temporary Xray process.
type Tester struct {
	XrayBin string
	mu      sync.Mutex
}

func (t *Tester) Test(servers []config.Server, isp []string) []TestResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	res := make([]TestResult, len(servers))
	for i, s := range servers {
		res[i] = TestResult{ID: s.ID, LatencyMs: -1}
	}
	if len(servers) == 0 {
		return res
	}
	if env.Mock {
		return mockResults(servers)
	}
	const base = 20000
	cfg, ports, err := BuildTestConfig(servers, base, isp)
	if err != nil {
		for i := range res {
			res[i].Error = err.Error()
		}
		return res
	}
	path := filepath.Join(env.DataDir, "xray-test.json")
	os.MkdirAll(env.DataDir, 0o700)
	if err := os.WriteFile(path, cfg, 0o600); err != nil {
		return res
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, t.XrayBin, "run", "-c", path)
	cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET=/usr/share/xray")
	tail := &tailBuffer{max: 4096}
	cmd.Stdout, cmd.Stderr = tail, tail
	if err := cmd.Start(); err != nil {
		for i := range res {
			res[i].Error = err.Error()
		}
		return res
	}
	defer cmd.Wait()

	// Wait until the first inbound accepts connections.
	ready := false
	for i := 0; i < 50 && !ready; i++ {
		for _, p := range ports {
			if p > 0 {
				if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 100*time.Millisecond); err == nil {
					c.Close()
					ready = true
				}
				break
			}
		}
		if !ready {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if !ready {
		msg := lastLine(tail.String())
		for i := range res {
			res[i].Error = "xray: " + msg
		}
		return res
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 8) // the router CPU is small
	for i, p := range ports {
		if p <= 0 {
			res[i].Error = "invalid link"
			continue
		}
		wg.Add(1)
		go func(i, p int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			d, err := ProbeSocks(p, 10*time.Second)
			if err != nil {
				res[i].Error = err.Error()
				return
			}
			res[i].LatencyMs = int(d.Milliseconds())
		}(i, p)
	}
	wg.Wait()
	return res
}

type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func lastLine(s string) string {
	lines := splitLines(s)
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i] != "" {
			return lines[i]
		}
	}
	return "did not start"
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
