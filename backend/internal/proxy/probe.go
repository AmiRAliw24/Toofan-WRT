package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"toofanwrt/internal/config"
	"toofanwrt/internal/env"
	"toofanwrt/internal/geo"
)

// Ping is measured against Google, falling back to Cloudflare. Plain HTTP:
// a TLS handshake through the tunnel would only add noise and seconds.
var probeURLs = []string{
	"http://www.google.com/generate_204",
	"http://cp.cloudflare.com/generate_204",
}

// ProbeSocks sends real traffic through a local SOCKS port. The first request
// opens the tunnel; the second one, on the same connection, is what users
// feel as "ping" while browsing.
func ProbeSocks(port int, timeout time.Duration) (time.Duration, error) {
	pu, _ := url.Parse(fmt.Sprintf("socks5h://127.0.0.1:%d", port))
	tr := &http.Transport{Proxy: http.ProxyURL(pu), MaxIdleConns: 1, ResponseHeaderTimeout: timeout}
	defer tr.CloseIdleConnections()
	cl := &http.Client{Transport: tr, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var lastErr error
	for _, u := range probeURLs {
		cold, err := get(cl, u)
		if err != nil {
			lastErr = err
			if errors.As(err, new(statusErr)) {
				continue // the site refused; try the next one
			}
			break // the tunnel itself failed: another site won't help
		}
		if warm, err := get(cl, u); err == nil {
			return warm, nil
		}
		return cold, nil
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
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return 0, statusErr(resp.StatusCode)
	}
	return time.Since(start), nil
}

type statusErr int

func (e statusErr) Error() string { return fmt.Sprintf("HTTP %d", int(e)) }

func simplifyErr(err error) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return errors.New("timeout")
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) {
		return errors.New("server did not answer")
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
	Skipped   bool   `json:"skipped,omitempty"` // OpenVPN: only measured while active
}

// Tester measures servers through one long-lived Xray process that has every
// server loaded on its own SOCKS port. It is rebuilt only when the server list
// changes and stops itself when idle, so a test costs one round trip per
// server instead of an Xray start.
type Tester struct {
	XrayBin   string
	CompatBin string // used when a loaded server needs it (NeedsCompatEngine)

	mu      sync.Mutex // one test at a time
	procMu  sync.Mutex
	fp      string
	ports   map[string]int
	perrs   map[string]string
	cancel  context.CancelFunc
	done    chan struct{}
	tail    *tailBuffer
	lastUse time.Time
	idle    *time.Timer
}

const testerIdle = 10 * time.Minute

// Test measures the servers in ids (all when nil) and calls onResult as soon
// as each one answers. all is the full server list, so the process can be
// kept across tests of different subsets.
func (t *Tester) Test(all []config.Server, ids []string, isp []string, onResult func(TestResult)) []TestResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	var xs, want []config.Server
	var out []TestResult
	for _, s := range all {
		sel := ids == nil || contains(ids, s.ID)
		if IsOpenVPN(s) {
			if sel {
				out = append(out, TestResult{ID: s.ID, Skipped: true})
			}
			continue
		}
		xs = append(xs, s)
		if sel {
			want = append(want, s)
		}
	}
	if len(want) == 0 {
		return out
	}
	emit := func(r TestResult) {
		if onResult != nil {
			onResult(r)
		}
	}
	if env.Mock {
		for _, r := range mockResults(want) {
			time.Sleep(time.Duration(80+rand.Intn(250)) * time.Millisecond)
			emit(r)
			out = append(out, r)
		}
		return out
	}
	ports, perrs, err := t.ensure(xs, isp)
	if err != nil {
		for _, s := range want {
			r := TestResult{ID: s.ID, LatencyMs: -1, Error: err.Error()}
			emit(r)
			out = append(out, r)
		}
		return out
	}

	var wg sync.WaitGroup
	var omu sync.Mutex
	sem := make(chan struct{}, 16)
	for _, s := range want {
		p := ports[s.ID]
		if p <= 0 {
			msg := perrs[s.ID]
			if msg == "" {
				msg = "invalid link"
			}
			r := TestResult{ID: s.ID, LatencyMs: -1, Error: msg}
			emit(r)
			out = append(out, r)
			continue
		}
		wg.Add(1)
		go func(id string, p int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := TestResult{ID: id, LatencyMs: -1}
			if d, err := ProbeSocks(p, 6*time.Second); err != nil {
				r.Error = err.Error()
			} else {
				r.LatencyMs = max(1, int(d.Milliseconds()))
			}
			emit(r)
			omu.Lock()
			out = append(out, r)
			omu.Unlock()
		}(s.ID, p)
	}
	wg.Wait()
	t.touch()
	return out
}

// ensure starts (or reuses) the tester Xray for this server list.
func (t *Tester) ensure(servers []config.Server, isp []string) (map[string]int, map[string]string, error) {
	t.procMu.Lock()
	defer t.procMu.Unlock()
	h := sha256.New()
	for _, s := range servers {
		fmt.Fprintf(h, "%s\x00%s\x00", s.ID, s.Link)
	}
	fmt.Fprint(h, isp, XrayVersion)
	fp := hex.EncodeToString(h.Sum(nil))
	if t.done != nil && fp == t.fp {
		select {
		case <-t.done: // crashed: start again below
		default:
			t.lastUse = time.Now()
			return t.ports, t.perrs, nil
		}
	}
	t.stopLocked()

	const base = 20000
	cfg, plist, elist, err := BuildTestConfig(servers, base, isp)
	if err != nil {
		return nil, nil, err
	}
	ports, perrs := map[string]int{}, map[string]string{}
	first := 0
	for i, s := range servers {
		ports[s.ID], perrs[s.ID] = plist[i], elist[i]
		if first == 0 && plist[i] > 0 {
			first = plist[i]
		}
	}
	if first == 0 {
		return ports, perrs, nil // nothing Xray could load
	}
	bin := t.XrayBin
	if t.CompatBin != "" {
		for _, s := range servers {
			if NeedsCompatEngine(s) {
				bin = t.CompatBin // it runs every server type
				break
			}
		}
	}
	path := filepath.Join(env.DataDir, "xray-test.json")
	os.MkdirAll(env.DataDir, 0o700)
	if err := os.WriteFile(path, cfg, 0o600); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "run", "-c", path)
	cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+geo.AssetDir())
	tail := &tailBuffer{max: 4096}
	cmd.Stdout, cmd.Stderr = tail, tail
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, err
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()

	ready := false
	for i := 0; i < 200 && !ready; i++ { // up to ~20s on slow routers
		select {
		case <-done:
			cancel()
			return nil, nil, errors.New("xray: " + lastLine(tail.String()))
		default:
		}
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", first), 100*time.Millisecond); err == nil {
			c.Close()
			ready = true
		} else {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if !ready {
		cancel()
		<-done
		return nil, nil, errors.New("xray: " + lastLine(tail.String()))
	}
	t.fp, t.ports, t.perrs = fp, ports, perrs
	t.cancel, t.done, t.tail = cancel, done, tail
	t.lastUse = time.Now()
	if t.idle == nil {
		t.idle = time.AfterFunc(testerIdle, t.idleCheck)
	} else {
		t.idle.Reset(testerIdle)
	}
	return ports, perrs, nil
}

func (t *Tester) touch() {
	t.procMu.Lock()
	t.lastUse = time.Now()
	t.procMu.Unlock()
}

func (t *Tester) idleCheck() {
	t.procMu.Lock()
	defer t.procMu.Unlock()
	if left := testerIdle - time.Since(t.lastUse); left > 0 {
		t.idle.Reset(left)
		return
	}
	t.stopLocked()
}

// Stop ends the tester process.
func (t *Tester) Stop() {
	t.procMu.Lock()
	defer t.procMu.Unlock()
	t.stopLocked()
}

func (t *Tester) stopLocked() {
	if t.cancel != nil {
		t.cancel()
		<-t.done
	}
	t.cancel, t.done, t.fp = nil, nil, ""
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
