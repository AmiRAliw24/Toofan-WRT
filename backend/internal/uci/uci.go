// Package uci is a thin wrapper around the uci command line tool.
package uci

import (
	"fmt"
	"strings"
	"time"

	"toofanwrt/internal/env"
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

// Batch collects uci changes and commits the touched configs.
// Deletes run first, one by one, and a missing option is not an error:
// "uci batch" would fail on it, so a re-run of the same change would break.
type Batch struct {
	deletes []string
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

// Delete removes key before any Set/AddList of this batch is applied.
func (b *Batch) Delete(key string) *Batch {
	b.deletes = append(b.deletes, key)
	b.track(key)
	return b
}

func (b *Batch) AddList(key, value string) *Batch {
	b.lines = append(b.lines, fmt.Sprintf("add_list %s=%s", key, quote(value)))
	b.track(key)
	return b
}

func (b *Batch) Empty() bool { return len(b.lines) == 0 && len(b.deletes) == 0 }

func (b *Batch) Commit() error {
	if b.Empty() {
		return nil
	}
	script := strings.Join(b.lines, "\n")
	if script != "" {
		script += "\n"
	}
	for c := range b.commits {
		script += "commit " + c + "\n"
	}
	if env.Mock {
		env.Logf("uci (mock): delete %v\n%s", b.deletes, script)
		return nil
	}
	for _, k := range b.deletes {
		env.Run(5*time.Second, "uci", "-q", "delete", k) // missing is fine
	}
	_, err := env.RunInput(15*time.Second, script, "uci", "batch")
	return err
}
