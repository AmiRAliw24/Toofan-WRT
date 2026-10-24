package wifi

import (
	"encoding/json"
	"errors"
	"math"
	"math/rand"
	"sort"
	"strconv"

	"toofanwrt/internal/env"
)

// ChannelScore is the measured congestion of one candidate channel.
type ChannelScore struct {
	Channel int     `json:"channel"`
	APs     int     `json:"aps"`
	Score   float64 `json:"score"` // lower is better (sum of neighbours' power, in mW)
}

type ScanResult struct {
	Band     string         `json:"band"`
	Current  int            `json:"current"`
	Best     int            `json:"best"`
	Channels []ChannelScore `json:"channels"`
	Networks int            `json:"networks"`
	Changed  bool           `json:"changed"`
}

var (
	cand24 = []int{1, 6, 11}
	cand5  = []int{36, 40, 44, 48, 149, 153, 157, 161, 165} // non-DFS: no radar wait
)

// apDevice returns the iwinfo device serving the band in AP mode.
func apDevice(band string) (string, int, error) {
	out, err := env.Ubus("iwinfo", "devices", "")
	if err != nil {
		return "", 0, err
	}
	var d struct {
		Devices []string `json:"devices"`
	}
	json.Unmarshal([]byte(out), &d)
	for _, dev := range d.Devices {
		info, err := env.Ubus("iwinfo", "info", `{"device":"`+dev+`"}`)
		if err != nil {
			continue
		}
		var i struct {
			Mode      string `json:"mode"`
			Frequency int    `json:"frequency"`
			Channel   int    `json:"channel"`
		}
		json.Unmarshal([]byte(info), &i)
		if i.Mode == "Master" && (i.Frequency > 4000) == (band == "5g") {
			return dev, i.Channel, nil
		}
	}
	return "", 0, errors.New("no access point on this band")
}

// overlap tells how strongly a neighbour on ch disturbs candidate c.
func overlap(band string, c, ch int) float64 {
	d := c - ch
	if d < 0 {
		d = -d
	}
	if band == "2g" {
		if d >= 5 {
			return 0
		}
		return 1 - float64(d)*0.2
	}
	// 5 GHz neighbours using 40/80 MHz share blocks of 4 channels (20 MHz each).
	if d == 0 {
		return 1
	}
	if (c-36)/16 == (ch-36)/16 && c < 149 == (ch < 149) {
		return 0.5
	}
	return 0
}

// Scan surveys nearby networks and ranks the candidate channels.
func Scan(band string) (ScanResult, error) {
	res := ScanResult{Band: band}
	cands := cand24
	if band == "5g" {
		cands = cand5
	}
	type ap struct {
		ch  int
		dbm int
	}
	var aps []ap
	if env.Mock {
		res.Current = map[string]int{"2g": 1, "5g": 36}[band]
		for i := 0; i < 14; i++ {
			c := cands[rand.Intn(len(cands))]
			aps = append(aps, ap{c, -45 - rand.Intn(45)})
		}
	} else {
		dev, cur, err := apDevice(band)
		if err != nil {
			return res, err
		}
		res.Current = cur
		out, err := env.Ubus("iwinfo", "scan", `{"device":"`+dev+`"}`)
		if err != nil {
			return res, err
		}
		var sr struct {
			Results []struct {
				Channel int `json:"channel"`
				Signal  int `json:"signal"`
			} `json:"results"`
		}
		json.Unmarshal([]byte(out), &sr)
		for _, r := range sr.Results {
			aps = append(aps, ap{r.Channel, r.Signal})
		}
		// Only channels the radio allows in this country.
		if allowed := channels(radioFor(band)); len(allowed) > 0 {
			ok := map[int]bool{}
			for _, c := range allowed {
				ok[c] = true
			}
			var f []int
			for _, c := range cands {
				if ok[c] {
					f = append(f, c)
				}
			}
			if len(f) > 0 {
				cands = f
			}
		}
	}
	res.Networks = len(aps)
	for _, c := range cands {
		cs := ChannelScore{Channel: c}
		for _, a := range aps {
			w := overlap(band, c, a.ch)
			if w > 0 {
				if a.ch == c {
					cs.APs++
				}
				cs.Score += w * math.Pow(10, float64(a.dbm)/10) * 1e6 // nW, readable numbers
			}
		}
		cs.Score = math.Round(cs.Score*100) / 100
		res.Channels = append(res.Channels, cs)
	}
	sort.Slice(res.Channels, func(i, j int) bool { return res.Channels[i].Score < res.Channels[j].Score })
	if len(res.Channels) > 0 {
		res.Best = res.Channels[0].Channel
	}
	return res, nil
}

func radioFor(band string) string {
	st, err := Get()
	if err != nil {
		return ""
	}
	for _, b := range st.Bands {
		if b.Band == band {
			return b.Radio
		}
	}
	return ""
}

// Optimize scans and switches to the best channel when it is clearly
// better (half the interference or less). Name, password and security are
// never touched.
func Optimize(band string, apply bool) (ScanResult, error) {
	res, err := Scan(band)
	if err != nil || !apply || res.Best == 0 || res.Best == res.Current {
		return res, err
	}
	var cur, best float64 = -1, 0
	for _, c := range res.Channels {
		if c.Channel == res.Current {
			cur = c.Score
		}
		if c.Channel == res.Best {
			best = c.Score
		}
	}
	if cur >= 0 && best > cur*0.5 {
		return res, nil
	}
	if err := SetChannel(band, res.Best); err != nil {
		return res, err
	}
	res.Changed = true
	env.Logf("wifi: %s channel %s -> %d", band, strconv.Itoa(res.Current), res.Best)
	return res, nil
}
