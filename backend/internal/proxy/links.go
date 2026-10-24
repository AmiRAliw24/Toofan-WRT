// Package proxy turns share links into Xray outbounds and runs the engine.
package proxy

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"toofanwrt/internal/config"
)

// ParseLinks extracts every supported link from pasted text or a
// subscription body (plain or base64). Bad lines are reported, not fatal.
func ParseLinks(text string) (servers []config.Server, bad []string) {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "[Interface]") {
		link, err := WGConfToLink(text, "")
		if err != nil {
			return nil, []string{"wireguard: " + err.Error()}
		}
		s, err := ParseLink(link)
		if err != nil {
			return nil, []string{"wireguard: " + err.Error()}
		}
		return []config.Server{s}, nil
	}
	if !strings.Contains(text, "://") {
		if dec, err := decodeB64(text); err == nil {
			text = string(dec)
		}
	}
	for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' || r == ' ' }) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		s, err := ParseLink(line)
		if err != nil {
			bad = append(bad, fmt.Sprintf("%s: %v", short(line), err))
			continue
		}
		servers = append(servers, s)
	}
	return
}

func short(s string) string {
	if len(s) > 48 {
		return s[:48] + "…"
	}
	return s
}

// ParseLink validates one link and fills the summary fields.
func ParseLink(link string) (config.Server, error) {
	ob, name, err := Outbound(link)
	if err != nil {
		return config.Server{}, err
	}
	s := config.Server{Link: link, Name: name, Protocol: ob.Protocol, LatencyMs: 0}
	s.Address, s.Port = ob.addr, ob.port
	if ob.StreamSettings != nil {
		s.Network = ob.StreamSettings.Network
		s.Security = ob.StreamSettings.Security
	}
	h := sha1.Sum([]byte(link))
	s.ID = hex.EncodeToString(h[:6])
	if s.Name == "" {
		s.Name = fmt.Sprintf("%s:%d", s.Address, s.Port)
	}
	return s, nil
}

type XOutbound struct {
	Tag            string          `json:"tag"`
	Protocol       string          `json:"protocol"`
	Settings       any             `json:"settings,omitempty"`
	StreamSettings *StreamSettings `json:"streamSettings,omitempty"`
	Mux            map[string]any  `json:"mux,omitempty"`

	addr string
	port int
}

type StreamSettings struct {
	Network             string         `json:"network,omitempty"`
	Security            string         `json:"security,omitempty"`
	TLSSettings         map[string]any `json:"tlsSettings,omitempty"`
	RealitySettings     map[string]any `json:"realitySettings,omitempty"`
	RawSettings         map[string]any `json:"rawSettings,omitempty"`
	WSSettings          map[string]any `json:"wsSettings,omitempty"`
	GRPCSettings        map[string]any `json:"grpcSettings,omitempty"`
	XHTTPSettings       map[string]any `json:"xhttpSettings,omitempty"`
	HTTPUpgradeSettings map[string]any `json:"httpupgradeSettings,omitempty"`
	KCPSettings         map[string]any `json:"kcpSettings,omitempty"`
	Sockopt             map[string]any `json:"sockopt,omitempty"`
}

// Outbound converts a share link to an Xray outbound object.
func Outbound(link string) (*XOutbound, string, error) {
	i := strings.Index(link, "://")
	if i < 0 {
		return nil, "", errors.New("not a link")
	}
	switch strings.ToLower(link[:i]) {
	case "vless":
		return parseVless(link)
	case "vmess":
		return parseVmess(link)
	case "trojan":
		return parseTrojan(link)
	case "ss":
		return parseSS(link)
	case "wireguard", "wg":
		return parseWireGuard(link)
	case "openvpn":
		return openvpnOutbound(link)
	}
	return nil, "", fmt.Errorf("unsupported protocol %q", link[:i])
}

func splitHostPort(u *url.URL) (string, int, error) {
	host := u.Hostname()
	port, err := strconv.Atoi(u.Port())
	if host == "" || err != nil || port <= 0 || port > 65535 {
		return "", 0, errors.New("bad address or port")
	}
	return host, port, nil
}

func fragmentName(u *url.URL) string {
	name, err := url.PathUnescape(u.Fragment)
	if err != nil {
		name = u.Fragment
	}
	return strings.TrimSpace(name)
}

func parseVless(link string) (*XOutbound, string, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, "", err
	}
	host, port, err := splitHostPort(u)
	if err != nil {
		return nil, "", err
	}
	id := u.User.Username()
	if id == "" {
		return nil, "", errors.New("missing uuid")
	}
	q := u.Query()
	user := map[string]any{"id": id, "encryption": def(q.Get("encryption"), "none")}
	if f := q.Get("flow"); f != "" {
		user["flow"] = f
	}
	ob := &XOutbound{
		Protocol: "vless",
		Settings: map[string]any{"vnext": []any{map[string]any{
			"address": host, "port": port, "users": []any{user},
		}}},
		addr: host, port: port,
	}
	ss, err := streamFromQuery(q, host)
	if err != nil {
		return nil, "", err
	}
	ob.StreamSettings = ss
	return ob, fragmentName(u), nil
}

func parseTrojan(link string) (*XOutbound, string, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, "", err
	}
	host, port, err := splitHostPort(u)
	if err != nil {
		return nil, "", err
	}
	pw := u.User.Username()
	if pw == "" {
		return nil, "", errors.New("missing password")
	}
	q := u.Query()
	if q.Get("security") == "" {
		q.Set("security", "tls")
	}
	ob := &XOutbound{
		Protocol: "trojan",
		Settings: map[string]any{"servers": []any{map[string]any{
			"address": host, "port": port, "password": pw,
		}}},
		addr: host, port: port,
	}
	ss, err := streamFromQuery(q, host)
	if err != nil {
		return nil, "", err
	}
	ob.StreamSettings = ss
	return ob, fragmentName(u), nil
}

func parseVmess(link string) (*XOutbound, string, error) {
	raw := strings.TrimPrefix(link[strings.Index(link, "://")+3:], "")
	if i := strings.Index(raw, "#"); i >= 0 {
		raw = raw[:i]
	}
	dec, err := decodeB64(raw)
	if err != nil {
		return nil, "", errors.New("bad vmess base64")
	}
	var v map[string]any
	if err := json.Unmarshal(dec, &v); err != nil {
		return nil, "", errors.New("bad vmess json")
	}
	str := func(k string) string {
		switch x := v[k].(type) {
		case string:
			return x
		case float64:
			return strconv.Itoa(int(x))
		}
		return ""
	}
	host := str("add")
	port, _ := strconv.Atoi(str("port"))
	if host == "" || port <= 0 {
		return nil, "", errors.New("bad address or port")
	}
	aid, _ := strconv.Atoi(str("aid"))
	ob := &XOutbound{
		Protocol: "vmess",
		Settings: map[string]any{"vnext": []any{map[string]any{
			"address": host, "port": port,
			"users": []any{map[string]any{"id": str("id"), "alterId": aid, "security": def(str("scy"), "auto")}},
		}}},
		addr: host, port: port,
	}
	q := url.Values{}
	q.Set("type", def(str("net"), "tcp"))
	q.Set("security", str("tls"))
	q.Set("sni", str("sni"))
	q.Set("host", str("host"))
	q.Set("path", str("path"))
	q.Set("alpn", str("alpn"))
	q.Set("fp", str("fp"))
	q.Set("headerType", str("type"))
	if q.Get("type") == "grpc" {
		q.Set("serviceName", str("path"))
		q.Set("mode", str("type"))
	}
	ss, err := streamFromQuery(q, host)
	if err != nil {
		return nil, "", err
	}
	ob.StreamSettings = ss
	return ob, str("ps"), nil
}

func parseSS(link string) (*XOutbound, string, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, "", err
	}
	var method, pw string
	if u.User != nil {
		if p, ok := u.User.Password(); ok {
			method, pw = u.User.Username(), p
		} else if dec, err := decodeB64(u.User.Username()); err == nil {
			method, pw, _ = strings.Cut(string(dec), ":")
		}
	}
	host, port, err := splitHostPort(u)
	if err != nil {
		// legacy form: ss://base64(method:pass@host:port)#name
		rest := link[5:]
		if i := strings.Index(rest, "#"); i >= 0 {
			rest = rest[:i]
		}
		dec, derr := decodeB64(rest)
		if derr != nil {
			return nil, "", err
		}
		u2, perr := url.Parse("ss://" + string(dec))
		if perr != nil {
			return nil, "", err
		}
		method = u2.User.Username()
		pw, _ = u2.User.Password()
		if host, port, err = splitHostPort(u2); err != nil {
			return nil, "", err
		}
	}
	if method == "" || pw == "" {
		return nil, "", errors.New("missing method or password")
	}
	ob := &XOutbound{
		Protocol: "shadowsocks",
		Settings: map[string]any{"servers": []any{map[string]any{
			"address": host, "port": port, "method": method, "password": pw,
		}}},
		StreamSettings: &StreamSettings{Network: "raw"},
		addr:           host, port: port,
	}
	return ob, fragmentName(u), nil
}

// streamFromQuery builds transport + security settings from the common
// share-link query parameters used by v2rayN, Hiddify, NekoBox etc.
func streamFromQuery(q url.Values, host string) (*StreamSettings, error) {
	network := strings.ToLower(def(q.Get("type"), "tcp"))
	if network == "tcp" {
		network = "raw"
	}
	if network == "splithttp" {
		network = "xhttp"
	}
	ss := &StreamSettings{Network: network}
	hostHdr := q.Get("host")
	path := q.Get("path")
	switch network {
	case "raw":
		if q.Get("headerType") == "http" {
			req := map[string]any{"path": []string{def(path, "/")}}
			if hostHdr != "" {
				req["headers"] = map[string]any{"Host": strings.Split(hostHdr, ",")}
			}
			ss.RawSettings = map[string]any{"header": map[string]any{"type": "http", "request": req}}
		}
	case "ws":
		ws := map[string]any{"path": def(path, "/")}
		if hostHdr != "" {
			ws["host"] = hostHdr
		}
		ss.WSSettings = ws
	case "grpc":
		g := map[string]any{"serviceName": q.Get("serviceName")}
		if q.Get("mode") == "multi" {
			g["multiMode"] = true
		}
		if a := q.Get("authority"); a != "" {
			g["authority"] = a
		}
		ss.GRPCSettings = g
	case "xhttp":
		x := map[string]any{"path": def(path, "/")}
		if hostHdr != "" {
			x["host"] = hostHdr
		}
		if m := q.Get("mode"); m != "" {
			x["mode"] = m
		}
		if extra := q.Get("extra"); extra != "" {
			var e any
			if json.Unmarshal([]byte(extra), &e) == nil {
				x["extra"] = e
			}
		}
		ss.XHTTPSettings = x
	case "httpupgrade":
		h := map[string]any{"path": def(path, "/")}
		if hostHdr != "" {
			h["host"] = hostHdr
		}
		ss.HTTPUpgradeSettings = h
	case "kcp", "mkcp":
		ss.Network = "kcp"
		k := map[string]any{"header": map[string]any{"type": def(q.Get("headerType"), "none")}}
		if s := q.Get("seed"); s != "" {
			k["seed"] = s
		}
		ss.KCPSettings = k
	default:
		return nil, fmt.Errorf("unsupported transport %q", network)
	}

	sec := strings.ToLower(q.Get("security"))
	alpn := splitNonEmpty(q.Get("alpn"))
	switch sec {
	case "", "none":
	case "tls":
		ss.Security = "tls"
		t := map[string]any{"serverName": def(q.Get("sni"), def(hostHdr, host))}
		if fp := q.Get("fp"); fp != "" {
			t["fingerprint"] = fp
		}
		if len(alpn) > 0 {
			t["alpn"] = alpn
		}
		if q.Get("allowInsecure") == "1" || q.Get("insecure") == "1" {
			t["allowInsecure"] = true
		}
		if ech := q.Get("ech"); ech != "" {
			t["echConfigList"] = ech
		}
		ss.TLSSettings = t
	case "reality":
		ss.Security = "reality"
		pbk := q.Get("pbk")
		if pbk == "" {
			return nil, errors.New("reality link without public key (pbk)")
		}
		r := map[string]any{
			"serverName":  q.Get("sni"),
			"fingerprint": def(q.Get("fp"), "chrome"),
			"publicKey":   pbk,
			"shortId":     q.Get("sid"),
		}
		if spx := q.Get("spx"); spx != "" {
			r["spiderX"] = spx
		}
		if pqv := q.Get("pqv"); pqv != "" {
			r["mldsa65Verify"] = pqv
		}
		ss.RealitySettings = r
	default:
		return nil, fmt.Errorf("unsupported security %q", sec)
	}
	return ss, nil
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func def(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func decodeB64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer("\n", "", "\r", "", " ", "").Replace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("not base64")
}
