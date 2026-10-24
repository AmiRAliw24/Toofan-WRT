// Package env holds process-wide settings and a small command runner.
package env

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// Mock makes every platform package return simulated data instead of
// touching the system, so the panel can be developed on a normal PC.
var Mock bool

// DataDir is where runtime files (xray config, geo caches) are written.
var DataDir = "/var/run/toofanwrt"

// Run executes a command with a timeout and returns trimmed stdout.
func Run(timeout time.Duration, name string, args ...string) (string, error) {
	return RunInput(timeout, "", name, args...)
}

func RunInput(timeout time.Duration, stdin string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(out.String()), fmt.Errorf("%s: %s", name, msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// Ubus calls a ubus method and returns its JSON output.
func Ubus(path, method, args string) (string, error) {
	if args == "" {
		args = "{}"
	}
	return Run(10*time.Second, "ubus", "call", path, method, args)
}

func Logf(format string, a ...any) {
	log.Printf(format, a...)
}
