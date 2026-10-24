package gaming

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"separ/internal/env"
)

// SQM shapes the WAN slightly below line speed with CAKE so queues build
// on the router (where CAKE keeps them short and fair) instead of in the
// modem. Download shaping uses an IFB device fed from the WAN ingress.

const ifb = "ifb-separ"

func SQMAvailable() bool {
	if env.Mock {
		return true
	}
	if _, err := exec.LookPath("tc"); err != nil {
		return false
	}
	_, err := env.Run(5*time.Second, "sh", "-c", "lsmod | grep -q '^sch_cake' || find /lib/modules/$(uname -r) -name sch_cake.ko | grep -q .")
	return err == nil
}

// ApplySQM installs (or removes, when both rates are 0) CAKE on wanDev.
func ApplySQM(wanDev string, downKbit, upKbit int) error {
	if env.Mock {
		return nil
	}
	if wanDev == "" {
		return errors.New("WAN device unknown")
	}
	RemoveSQM(wanDev)
	if downKbit <= 0 && upKbit <= 0 {
		return nil
	}
	run := func(args ...string) error {
		_, err := env.Run(10*time.Second, args[0], args[1:]...)
		return err
	}
	if upKbit > 0 {
		if err := run("tc", "qdisc", "replace", "dev", wanDev, "root", "cake",
			"bandwidth", fmt.Sprintf("%dkbit", upKbit), "diffserv4", "dual-srchost", "nat", "ack-filter"); err != nil {
			return fmt.Errorf("upload shaper: %w", err)
		}
	}
	if downKbit > 0 {
		dev := ifb
		if err := run("ip", "link", "add", ifb, "type", "ifb"); err != nil && !strings.Contains(err.Error(), "exists") {
			// busybox ip can't create ifb links; use the module's own ifb0.
			env.Run(5*time.Second, "insmod", "ifb", "numifbs=1")
			if _, err := env.Run(3*time.Second, "ip", "link", "show", "ifb0"); err != nil {
				return errors.New("ifb device unavailable (install kmod-ifb)")
			}
			dev = "ifb0"
		}
		steps := [][]string{
			{"ip", "link", "set", dev, "up"},
			{"tc", "qdisc", "replace", "dev", dev, "root", "cake", "bandwidth", fmt.Sprintf("%dkbit", downKbit),
				"diffserv4", "dual-dsthost", "nat", "wash", "ingress"},
			{"tc", "qdisc", "add", "dev", wanDev, "handle", "ffff:", "ingress"},
			{"tc", "filter", "add", "dev", wanDev, "parent", "ffff:", "protocol", "all", "u32", "match", "u32", "0", "0",
				"action", "mirred", "egress", "redirect", "dev", dev},
		}
		for _, s := range steps {
			if err := run(s...); err != nil {
				RemoveSQM(wanDev)
				return fmt.Errorf("download shaper: %w", err)
			}
		}
	}
	return nil
}

func RemoveSQM(wanDev string) {
	if env.Mock || wanDev == "" {
		return
	}
	env.Run(5*time.Second, "tc", "qdisc", "del", "dev", wanDev, "root")
	env.Run(5*time.Second, "tc", "qdisc", "del", "dev", wanDev, "ingress")
	env.Run(5*time.Second, "ip", "link", "del", ifb)
	env.Run(5*time.Second, "tc", "qdisc", "del", "dev", "ifb0", "root")
}
