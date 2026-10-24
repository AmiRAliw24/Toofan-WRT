// Package uci is a thin wrapper around the uci command line tool.
package uci

import (
	"fmt"
	"strings"
	"time"

	"separ/internal/env"
)

// Show returns every option of a config as "config.section.option" -> value.
func Show(config string) (map[string]string, error) {
	out, err := env.Run(5*time.Second, "uci", "-q", "show", config)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m[k] = unquote(v)
	}
	return m, nil
}

// unquote handles uci's single-quoted values (lists are joined by spaces).
func unquote(v string) string {
	if strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'") && len(v) >= 2 {
		inner := v[1 : len(v)-1]
		return strings.ReplaceAll(inner, `'\''`, `'`)
	}
	return v
}

// Batch runs uci commands in one transaction and commits the touched configs.
type Batch struct {
	lines   []string
	commits map[string]bool
}

func NewBatch() *Batch { return &Batch{commits: map[string]bool{}} }

func quote(v string) string { return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'" }

func (b *Batch) track(key string) {
	cfg, _, _ := strings.Cut(key, ".")
	b.commits[cfg] = true
}

func (b *Batch) Set(key, value string) *Batch {
	b.lines = append(b.lines, fmt.Sprintf("set %s=%s", key, quote(value)))
	b.track(key)
	return b
}

func (b *Batch) Delete(key string) *Batch {
	b.lines = append(b.lines, "delete "+key)
	b.track(key)
	return b
}

func (b *Batch) AddList(key, value string) *Batch {
	b.lines = append(b.lines, fmt.Sprintf("add_list %s=%s", key, quote(value)))
	b.track(key)
	return b
}

func (b *Batch) Empty() bool { return len(b.lines) == 0 }

func (b *Batch) Commit() error {
	if len(b.lines) == 0 {
		return nil
	}
	script := strings.Join(b.lines, "\n") + "\n"
	for c := range b.commits {
		script += "commit " + c + "\n"
	}
	if env.Mock {
		env.Logf("uci batch (mock):\n%s", script)
		return nil
	}
	_, err := env.RunInput(15*time.Second, script, "uci", "batch")
	return err
}
