// Package fetch downloads release files (Xray cores, geo data) to disk:
// directly first, then through the tunnel's SOCKS port when GitHub is
// filtered on the line. Files go to flash, never into RAM.
package fetch

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// SocksPort is the tunnel's local SOCKS port (0 = no tunnel fallback).
var SocksPort int

func client(viaTunnel bool, timeout time.Duration) *http.Client {
	tr := &http.Transport{ResponseHeaderTimeout: 30 * time.Second}
	if viaTunnel {
		pu, _ := url.Parse(fmt.Sprintf("socks5h://127.0.0.1:%d", SocksPort))
		tr.Proxy = http.ProxyURL(pu)
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

// Get performs a GET, direct first and then via the tunnel. The caller
// closes the body. The response's final URL tells which release a
// "latest" link resolved to.
func Get(rawURL string, timeout time.Duration) (*http.Response, error) {
	var lastErr error
	for _, tunnel := range []bool{false, true} {
		if tunnel && SocksPort == 0 {
			break
		}
		req, _ := http.NewRequest(http.MethodGet, rawURL, nil)
		req.Header.Set("User-Agent", "ToofanWrt")
		resp, err := client(tunnel, timeout).Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		if err == nil {
			resp.Body.Close()
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		lastErr = err
	}
	return nil, lastErr
}

// Progress reports bytes done and the total (-1 when unknown).
type Progress func(done, total int64)

// ToFile downloads rawURL into dst (written via dst+".part" and renamed),
// refusing anything larger than max bytes. It returns the hex SHA-256 of
// the file and the URLs it was redirected through (see redirectChain).
func ToFile(rawURL, dst string, max int64, progress Progress) (sum, finalURL string, err error) {
	resp, err := Get(rawURL, 15*time.Minute)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.ContentLength > max {
		return "", "", fmt.Errorf("file too large (%d bytes)", resp.ContentLength)
	}
	part := dst + ".part"
	f, err := os.Create(part)
	if err != nil {
		return "", "", err
	}
	h := sha256.New()
	var done int64
	buf := make([]byte, 64<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			done += int64(n)
			if done > max {
				f.Close()
				os.Remove(part)
				return "", "", errors.New("file too large")
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				os.Remove(part)
				return "", "", werr
			}
			h.Write(buf[:n])
			if progress != nil {
				progress(done, resp.ContentLength)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(part)
			return "", "", rerr
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(part)
		return "", "", err
	}
	if err := os.Rename(part, dst); err != nil {
		return "", "", err
	}
	return hex.EncodeToString(h.Sum(nil)), redirectChain(resp), nil
}

// redirectChain lists every URL the request went through, newest first,
// space separated: GitHub's "latest" link names the release only in the
// first redirect, not in the final storage URL.
func redirectChain(resp *http.Response) string {
	var urls []string
	for r := resp.Request; r != nil; {
		urls = append(urls, r.URL.String())
		if r.Response == nil {
			break
		}
		r = r.Response.Request
	}
	return strings.Join(urls, " ")
}

// Text fetches a small text file (checksums, digests).
func Text(rawURL string) (string, error) {
	resp, err := Get(rawURL, 2*time.Minute)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return string(b), err
}

// FindSHA256 extracts the first 64-hex-digit SHA-256 from checksum text
// ("<hash>  name" lines, or Xray's "SHA2-256= <hash>" .dgst files).
func FindSHA256(text string) string {
	for _, f := range strings.FieldsFunc(text, func(r rune) bool { return r == ' ' || r == '\n' || r == '\t' || r == '=' || r == '\r' }) {
		f = strings.ToLower(f)
		if len(f) == 64 {
			if _, err := hex.DecodeString(f); err == nil {
				return f
			}
		}
	}
	return ""
}

// SHA256Line picks the SHA-256 for a specific algorithm label in an Xray
// .dgst file ("SHA2-256= <hash>"), ignoring SHA3 and others.
func SHA256Line(dgst string) string {
	for _, line := range strings.Split(dgst, "\n") {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(line)), "SHA2-256") {
			return FindSHA256(line)
		}
	}
	return FindSHA256(dgst)
}
