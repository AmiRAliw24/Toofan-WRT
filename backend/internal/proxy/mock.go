package proxy

import (
	"hash/fnv"
	"math/rand"

	"toofanwrt/internal/config"
)

func mockResults(servers []config.Server) []TestResult {
	out := make([]TestResult, len(servers))
	for i, s := range servers {
		h := fnv.New32a()
		h.Write([]byte(s.ID))
		v := h.Sum32()
		out[i] = TestResult{ID: s.ID, LatencyMs: 90 + int(v%300) + rand.Intn(25)}
		if v%5 == 0 {
			out[i] = TestResult{ID: s.ID, LatencyMs: -1, Error: "timeout"}
		}
	}
	return out
}
