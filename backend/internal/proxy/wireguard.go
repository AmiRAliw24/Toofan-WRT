package proxy

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// WireGuard runs inside Xray (its "wireguard" outbound), so it shares the
// transparent proxy, Iran split, kill switch and watchdog with every other
// server type and needs no kernel module.

// WGConfToLink converts a wg-quick style config into a wireguard:// link,
// which is how the server is stored.
func WGConfToLink(conf, name string) (string, error) {
	var section string
	iface := map[string]string{}
	peer := map[string]string{}
	for _, raw := range strings.Split(conf, "\n") {
		line := strings.TrimSpace(raw)
		if i := strings.IndexAny(line, "#;"); i == 0 {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Trim(line, "[] "))
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		switch section {
		case "interface":
			iface[k] = v
		case "peer":
			if _, dup := peer[k]; !dup { // first peer only
				peer[k] = v
			}
		}
	}
	if iface["privatekey"] == "" || peer["publickey"] == "" || peer["endpoint"] == "" {
		return "", errors.New("config needs PrivateKey, PublicKey and Endpoint")
	}
	host, port, err := net.SplitHostPort(peer["endpoint"])
	if err != nil {
		return "", fmt.Errorf("bad endpoint %q", peer["endpoint"])
	}
	q := url.Values{}
	q.Set("publickey", peer["publickey"])
	if v := peer["presharedkey"]; v != "" {
		q.Set("presharedkey", v)
	}
	q.Set("address", strings.ReplaceAll(iface["address"], " ", ""))
	if v := iface["mtu"]; v != "" {
		q.Set("mtu", v)
	}
	if v := peer["persistentkeepalive"]; v != "" {
		q.Set("keepalive", v)
	}
	if v := iface["reserved"]; v != "" { // used by Cloudflare WARP configs
		q.Set("reserved", strings.ReplaceAll(v, " ", ""))
	}
	if name == "" {
		name = "WireGuard " + host
	}
	u := url.URL{
		Scheme:   "wireguard",
		User:     url.User(iface["privatekey"]),
		Host:     net.JoinHostPort(host, port),
		RawQuery: q.Encode(),
		Fragment: name,
	}
	return u.String(), nil
}

func parseWireGuard(link string) (*XOutbound, string, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, "", err
	}
	host, port, err := splitHostPort(u)
	if err != nil {
		return nil, "", err
	}
	q := u.Query()
	first := func(keys ...string) string {
		for _, k := range keys {
			if v := q.Get(k); v != "" {
				return v
			}
		}
		return ""
	}
	priv := u.User.Username()
	if p, err := url.PathUnescape(priv); err == nil {
		priv = p
	}
	pub := first("publickey", "peer_public_key", "public_key", "pbk")
	if priv == "" || pub == "" {
		return nil, "", errors.New("missing private or public key")
	}
	addrs := splitNonEmpty(first("address", "local_address", "ip"))
	if len(addrs) == 0 {
		return nil, "", errors.New("missing interface address")
	}
	for i, a := range addrs {
		if !strings.Contains(a, "/") {
			if strings.Contains(a, ":") {
				addrs[i] = a + "/128"
			} else {
				addrs[i] = a + "/32"
			}
		}
	}
	p := map[string]any{"publicKey": pub, "endpoint": net.JoinHostPort(host, strconv.Itoa(port))}
	if psk := first("presharedkey", "pre_shared_key", "psk"); psk != "" {
		p["preSharedKey"] = psk
	}
	if ka, _ := strconv.Atoi(first("keepalive", "persistent_keepalive")); ka > 0 {
		p["keepAlive"] = ka
	}
	settings := map[string]any{
		"secretKey":      priv,
		"address":        addrs,
		"peers":          []any{p},
		"domainStrategy": "ForceIPv4",
	}
	if mtu, _ := strconv.Atoi(first("mtu")); mtu > 0 {
		settings["mtu"] = mtu
	} else {
		settings["mtu"] = 1280
	}
	if r := splitNonEmpty(first("reserved")); len(r) == 3 {
		var rs []int
		for _, x := range r {
			n, err := strconv.Atoi(x)
			if err != nil || n < 0 || n > 255 {
				return nil, "", errors.New("bad reserved bytes")
			}
			rs = append(rs, n)
		}
		settings["reserved"] = rs
	}
	ob := &XOutbound{Protocol: "wireguard", Settings: settings, addr: host, port: port}
	return ob, fragmentName(u), nil
}
