package proxy

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"separ/internal/config"
	"separ/internal/env"
)

// FetchSubscription downloads a subscription. It tries directly first and
// then through the running tunnel, since many panels are filtered.
func FetchSubscription(u string) (string, error) {
	if env.Mock {
		return mockSubscription(u), nil
	}
	pu, err := url.Parse(u)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") {
		return "", errors.New("invalid subscription URL")
	}
	try := func(tr *http.Transport) (string, error) {
		cl := &http.Client{Transport: tr, Timeout: 20 * time.Second}
		req, _ := http.NewRequest("GET", u, nil)
		// Panels return the plain link list for v2ray-style clients.
		req.Header.Set("User-Agent", "v2rayNG/1.9.0")
		resp, err := cl.Do(req)
		if err != nil {
			return "", simplifyErr(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		return string(b), err
	}
	body, err := try(&http.Transport{})
	if err == nil {
		return body, nil
	}
	sp, _ := url.Parse(fmt.Sprintf("socks5h://127.0.0.1:%d", SocksPort))
	if body2, err2 := try(&http.Transport{Proxy: http.ProxyURL(sp)}); err2 == nil {
		return body2, nil
	}
	return "", err
}

func SubID(u string) string {
	h := sha1.Sum([]byte(u))
	return hex.EncodeToString(h[:5])
}

// UpdateSubscriptions refreshes one subscription (or all when id is empty)
// and replaces its servers while keeping the user's manual servers.
func (m *Manager) UpdateSubscriptions(id string) error {
	if id == "" {
		m.mu.Lock()
		m.lastSubUpdate = time.Now()
		m.mu.Unlock()
	}
	cfg := m.Store.Get().Proxy
	var firstErr error
	for _, sub := range cfg.Subscriptions {
		if id != "" && sub.ID != id {
			continue
		}
		body, err := FetchSubscription(sub.URL)
		var servers []config.Server
		var bad []string
		if err == nil {
			servers, bad = ParseLinks(body)
			if len(servers) == 0 {
				err = errors.New("no valid servers in subscription")
				if len(bad) > 0 {
					err = fmt.Errorf("no valid servers: %s", bad[0])
				}
			}
		}
		subID := sub.ID
		upErr := m.Store.Update(func(c *config.Config) error {
			for i := range c.Proxy.Subscriptions {
				s := &c.Proxy.Subscriptions[i]
				if s.ID != subID {
					continue
				}
				s.UpdatedAt = time.Now().Unix()
				if err != nil {
					s.Error = err.Error()
					return nil
				}
				s.Error, s.Count = "", len(servers)
			}
			if err != nil {
				return nil
			}
			old := map[string]config.Server{}
			kept := c.Proxy.Servers[:0]
			for _, s := range c.Proxy.Servers {
				if s.SubID == subID {
					old[s.ID] = s
					continue
				}
				kept = append(kept, s)
			}
			for _, s := range servers {
				s.SubID = subID
				if o, ok := old[s.ID]; ok {
					s.LatencyMs, s.TestedAt, s.TestError = o.LatencyMs, o.TestedAt, o.TestError
				}
				if !hasServer(kept, s.ID) {
					kept = append(kept, s)
				}
			}
			c.Proxy.Servers = kept
			return nil
		})
		if err == nil {
			err = upErr
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
		m.mu.Lock()
		if err != nil {
			m.logEvent("warn", "subscription %s: %v", sub.Name, err)
		} else {
			m.logEvent("info", "subscription %s updated (%d servers)", sub.Name, len(servers))
		}
		m.mu.Unlock()
	}
	m.Kick()
	return firstErr
}

func hasServer(list []config.Server, id string) bool {
	for _, s := range list {
		if s.ID == id {
			return true
		}
	}
	return false
}

func mockSubscription(u string) string {
	names := []string{"🇩🇪 Germany", "🇳🇱 Netherlands", "🇫🇮 Finland", "🇹🇷 Turkey", "🇦🇪 UAE", "🇫🇷 France"}
	var b strings.Builder
	for i, n := range names {
		fmt.Fprintf(&b, "vless://2b3c1f6e-8d1a-4c7e-9f00-%012d@s%d.example.net:443?type=xhttp&security=reality&pbk=Q2hhbmdlTWVQbGVhc2VDaGFuZ2VNZVBsZWFzZTEyMzQ&sni=www.microsoft.com&fp=chrome&sid=ab12&path=%%2Fapi#%s\n", i, i, url.PathEscape(n))
	}
	return b.String()
}
