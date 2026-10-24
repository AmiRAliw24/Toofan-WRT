// Package geo reads v2ray geoip.dat / geosite.dat files (protobuf) without
// pulling in a protobuf library; only the few fields we need are decoded.
package geo

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
)

// Paths searched for the data files shipped by OpenWrt's v2ray-geodata packages.
var (
	GeoIPPaths   = []string{"/usr/share/xray/geoip.dat", "/usr/share/v2ray/geoip.dat"}
	IranDatPaths = []string{"/usr/share/xray/iran.dat", "/usr/share/v2ray/iran.dat"}
)

func FirstExisting(paths []string) string {
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.Size() > 0 {
			return p
		}
	}
	return ""
}

// CountryCIDRs returns the IPv4 networks of a country code from geoip.dat.
func CountryCIDRs(path, code string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	code = strings.ToUpper(code)
	var out []string
	err = eachField(data, func(num int, wire int, val []byte, _ uint64) error {
		if num != 1 || wire != 2 { // GeoIPList.entry
			return nil
		}
		var cc string
		var cidrs [][]byte
		eachField(val, func(n, w int, v []byte, _ uint64) error {
			switch {
			case n == 1 && w == 2:
				cc = string(v)
			case n == 2 && w == 2:
				cidrs = append(cidrs, v)
			}
			return nil
		})
		if strings.ToUpper(cc) != code {
			return nil
		}
		for _, c := range cidrs {
			var ip []byte
			var prefix uint64
			eachField(c, func(n, w int, v []byte, u uint64) error {
				switch {
				case n == 1 && w == 2:
					ip = v
				case n == 2 && w == 0:
					prefix = u
				}
				return nil
			})
			if len(ip) == 4 && prefix <= 32 {
				out = append(out, fmt.Sprintf("%s/%d", net.IP(ip).String(), prefix))
			}
		}
		return errStop
	})
	if err != nil && err != errStop {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("country %s not found in %s", code, path)
	}
	return out, nil
}

var errStop = errors.New("stop")

// eachField walks the top-level fields of a protobuf message.
func eachField(b []byte, fn func(num, wire int, val []byte, varint uint64) error) error {
	for len(b) > 0 {
		key, n := uvarint(b)
		if n <= 0 {
			return errors.New("bad protobuf key")
		}
		b = b[n:]
		num, wire := int(key>>3), int(key&7)
		switch wire {
		case 0:
			v, n := uvarint(b)
			if n <= 0 {
				return errors.New("bad varint")
			}
			b = b[n:]
			if err := fn(num, wire, nil, v); err != nil {
				return err
			}
		case 1:
			if len(b) < 8 {
				return errors.New("short fixed64")
			}
			b = b[8:]
		case 2:
			l, n := uvarint(b)
			if n <= 0 || uint64(len(b)-n) < l {
				return errors.New("bad length")
			}
			val := b[n : n+int(l)]
			b = b[n+int(l):]
			if err := fn(num, wire, val, 0); err != nil {
				return err
			}
		case 5:
			if len(b) < 4 {
				return errors.New("short fixed32")
			}
			b = b[4:]
		default:
			return fmt.Errorf("unsupported wire type %d", wire)
		}
	}
	return nil
}

func uvarint(b []byte) (uint64, int) {
	var x uint64
	var s uint
	for i, c := range b {
		if i == 10 {
			return 0, -1
		}
		if c < 0x80 {
			return x | uint64(c)<<s, i + 1
		}
		x |= uint64(c&0x7f) << s
		s += 7
	}
	return 0, 0
}
