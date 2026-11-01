package wifi

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"toofanwrt/internal/uci"
)

func TestFixWidth(t *testing.T) {
	for _, c := range []struct{ band, ch, in, want string }{
		{"2g", "6", "VHT80", "HT20"},
		{"2g", "6", "HT40", "HT40"},
		{"2g", "6", "VHT40", "HT40"},
		{"2g", "14", "HT40", "HT20"},
		{"5g", "36", "VHT80", "VHT80"},
		{"5g", "165", "VHT80", "VHT20"},
		{"5g", "149", "HE80", "VHT80"},
		{"5g", "144", "VHT80", "VHT40"},
		{"5g", "auto", "VHT80", "VHT80"},
		{"5g", "36", "", ""},
	} {
		if got := fixWidth(c.band, c.ch, c.in); got != c.want {
			t.Errorf("fixWidth(%s, %s, %s) = %s, want %s", c.band, c.ch, c.in, got, c.want)
		}
	}
	if !IsDFS(52) || !IsDFS(100) || IsDFS(36) || IsDFS(149) {
		t.Error("DFS range")
	}
}

func TestApplyRadiosRevertsWhenRadioFails(t *testing.T) {
	dir := t.TempDir()
	wirelessConf = filepath.Join(dir, "wireless")
	settle = 0
	defer func() {
		statusFn, reloadFn, commitFn = wirelessStatus, reloadWifi, func(b *uci.Batch) error { return b.Commit() }
	}()
	os.WriteFile(wirelessConf, []byte("channel 1\n"), 0o644)
	reloads := 0
	reloadFn = func() error { reloads++; return nil }
	commitFn = func(*uci.Batch) error { return os.WriteFile(wirelessConf, []byte("channel 13\n"), 0o644) }

	// Radio refuses the new channel: previous file back, Wi-Fi reloaded twice.
	statusFn = func() (map[string]radioStatus, error) {
		return map[string]radioStatus{"radio0": {Up: false, Failed: true}}, nil
	}
	if err := applyRadios(uci.NewBatch(), []string{"radio0"}, time.Second); err == nil {
		t.Fatal("expected an error")
	}
	if b, _ := os.ReadFile(wirelessConf); string(b) != "channel 1\n" || reloads != 2 {
		t.Fatalf("not restored: %q reloads=%d", b, reloads)
	}

	// Radio comes up: the new settings stay.
	reloads = 0
	statusFn = func() (map[string]radioStatus, error) { return map[string]radioStatus{"radio0": {Up: true}}, nil }
	if err := applyRadios(uci.NewBatch(), []string{"radio0"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(wirelessConf); string(b) != "channel 13\n" || reloads != 1 {
		t.Fatalf("changed: %q reloads=%d", b, reloads)
	}

	// Still starting at the deadline (no failure flag): restored too.
	os.WriteFile(wirelessConf, []byte("channel 1\n"), 0o644)
	statusFn = func() (map[string]radioStatus, error) {
		return map[string]radioStatus{"radio0": {Up: false, Pending: true}}, nil
	}
	if err := applyRadios(uci.NewBatch(), []string{"radio0"}, 1500*time.Millisecond); err == nil {
		t.Fatal("expected a timeout error")
	}
	if b, _ := os.ReadFile(wirelessConf); string(b) != "channel 1\n" {
		t.Fatalf("not restored after timeout: %q", b)
	}
}

// "wifi reload" can leave the access point on its old channel: the radio
// is restarted once, and if it still won't move the old settings return.
func TestApplyRadiosVerifiesChannel(t *testing.T) {
	dir := t.TempDir()
	wirelessConf = filepath.Join(dir, "wireless")
	settle, channelSettle = 0, 1500*time.Millisecond
	defer func(l func(string) (int, bool), r func(string)) {
		statusFn, reloadFn, commitFn = wirelessStatus, reloadWifi, func(b *uci.Batch) error { return b.Commit() }
		liveChannelFn, restartRadioFn, channelSettle = l, r, 10*time.Second
	}(liveChannelFn, restartRadioFn)
	os.WriteFile(wirelessConf, []byte("channel 1\n"), 0o644)
	reloadFn = func() error { return nil }
	commitFn = func(*uci.Batch) error { return os.WriteFile(wirelessConf, []byte("channel 11\n"), 0o644) }
	statusFn = func() (map[string]radioStatus, error) { return map[string]radioStatus{"radio1": {Up: true}}, nil }

	var live atomic.Int32
	live.Store(1)
	restarts := 0
	liveChannelFn = func(string) (int, bool) { return int(live.Load()), true }
	restartRadioFn = func(string) { restarts++; live.Store(11) }
	if err := applyRadiosCh(uci.NewBatch(), []string{"radio1"}, 3*time.Second, map[string]int{"radio1": 11}); err != nil {
		t.Fatal(err)
	}
	if restarts != 1 {
		t.Fatalf("radio restarts = %d, want 1", restarts)
	}
	if b, _ := os.ReadFile(wirelessConf); string(b) != "channel 11\n" {
		t.Fatalf("new channel not kept: %q", b)
	}

	// Never moves: previous settings back, error names the channel it stayed on.
	os.WriteFile(wirelessConf, []byte("channel 1\n"), 0o644)
	live.Store(1)
	restarts = 0
	restartRadioFn = func(string) { restarts++ }
	err := applyRadiosCh(uci.NewBatch(), []string{"radio1"}, 3*time.Second, map[string]int{"radio1": 11})
	if err == nil || !strings.Contains(err.Error(), "stayed on channel 1") {
		t.Fatalf("got %v", err)
	}
	if b, _ := os.ReadFile(wirelessConf); string(b) != "channel 1\n" {
		t.Fatalf("not restored: %q", b)
	}

	// Moves by itself after a moment: no restart needed.
	os.WriteFile(wirelessConf, []byte("channel 1\n"), 0o644)
	live.Store(1)
	restarts = 0
	go func() { time.Sleep(300 * time.Millisecond); live.Store(11) }()
	if err := applyRadiosCh(uci.NewBatch(), []string{"radio1"}, 3*time.Second, map[string]int{"radio1": 11}); err != nil || restarts != 0 {
		t.Fatalf("err=%v restarts=%d", err, restarts)
	}
}
