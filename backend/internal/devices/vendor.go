package devices

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
	"sort"
	"strings"
	"sync"
)

// oui.gz: "AABBCC\tVendor" lines sorted by prefix, built from Wireshark's
// manuf file (IEEE registry), /24 blocks only.
//
//go:embed data/oui.gz
var ouiGz []byte

var (
	ouiOnce  sync.Once
	ouiData  []byte
	ouiLines []int32 // start offset of each line
)

func loadOUI() {
	r, err := gzip.NewReader(bytes.NewReader(ouiGz))
	if err != nil {
		return
	}
	ouiData, _ = io.ReadAll(r)
	ouiLines = append(ouiLines, 0)
	for i, c := range ouiData {
		if c == '\n' && i+1 < len(ouiData) {
			ouiLines = append(ouiLines, int32(i+1))
		}
	}
}

// Vendor returns the manufacturer registered for a MAC address, "" when
// unknown. Random (private) addresses have none.
func Vendor(mac string) string {
	if isRandomMAC(mac) {
		return ""
	}
	p := strings.ToUpper(strings.NewReplacer(":", "", "-", "").Replace(mac))
	if len(p) < 6 {
		return ""
	}
	p = p[:6]
	ouiOnce.Do(loadOUI)
	line := func(i int) []byte {
		start := int(ouiLines[i])
		end := bytes.IndexByte(ouiData[start:], '\n')
		if end < 0 {
			return ouiData[start:]
		}
		return ouiData[start : start+end]
	}
	i := sort.Search(len(ouiLines), func(i int) bool { return string(line(i)[:6]) >= p })
	if i < len(ouiLines) {
		if l := line(i); string(l[:6]) == p && len(l) > 7 {
			return string(l[7:])
		}
	}
	return ""
}
