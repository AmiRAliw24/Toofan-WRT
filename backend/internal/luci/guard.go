package luci

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"toofanwrt/internal/env"
	"toofanwrt/internal/uci"
)

// ToofanWrt owns the router's web ports. Whatever web interface gets
// installed later (LuCI's uhttpd, nginx) is moved aside to AltPort and stays
// usable there as the secondary interface.

var (
	procDir    = "/proc"
	uhttpdConf = "/etc/config/uhttpd"
	selfPID    = os.Getpid
)

// webPort reports whether a uhttpd listen value ("0.0.0.0:80", "[::]:443",
// "80") uses one of the panel's ports.
func webPort(listen string, ports map[int]bool) bool {
	for _, l := range strings.Fields(listen) {
		i := strings.LastIndex(l, ":")
		p, err := strconv.Atoi(l[i+1:])
		if err == nil && ports[p] {
			return true
		}
	}
	return false
}

// uhttpdOnOurPorts lists the uhttpd sections that listen on 80/443.
func uhttpdOnOurPorts(conf map[string]string) []string {
	ports := map[int]bool{80: true, 443: true}
	var secs []string
	seen := map[string]bool{}
	for k, v := range conf {
		parts := strings.Split(k, ".")
		if len(parts) != 3 || (parts[2] != "listen_http" && parts[2] != "listen_https") {
			continue
		}
		if webPort(v, ports) && !seen[parts[1]] {
			seen[parts[1]] = true
			secs = append(secs, parts[1])
		}
	}
	return secs
}

// Guard moves uhttpd off ports 80 and 443 when a LuCI install (or anyone)
// put it there, and restarts it on AltPort if it was running. It reports
// whether anything changed.
func Guard() (bool, error) {
	if env.Mock {
		return false, nil
	}
	if _, err := os.Stat(uhttpdConf); err != nil {
		return false, nil
	}
	conf, err := uci.Show("uhttpd")
	if err != nil {
		return false, err
	}
	secs := uhttpdOnOurPorts(conf)
	if len(secs) == 0 {
		return false, nil
	}
	// A freshly installed LuCI is enabled at boot and may have just failed
	// to start because the panel holds port 80: bring it up on its side
	// port as well.
	running := PortHolderName(80) == "uhttpd" || PortHolderName(AltPort) == "uhttpd" || pidOf("uhttpd") || bootEnabled("uhttpd")
	b := uci.NewBatch()
	for i, s := range secs {
		b.Delete("uhttpd." + s + ".listen_http").Delete("uhttpd." + s + ".listen_https")
		if i == 0 {
			b.AddList("uhttpd."+s+".listen_http", "0.0.0.0:"+strconv.Itoa(AltPort))
		}
		b.Set("uhttpd."+s+".redirect_https", "0")
	}
	if err := b.Commit(); err != nil {
		return false, err
	}
	env.Logf("web guard: moved uhttpd (%s) to port %d; ToofanWrt keeps 80", strings.Join(secs, ","), AltPort)
	if running {
		env.Run(20*time.Second, "/etc/init.d/uhttpd", "restart")
	}
	return true, nil
}

// FreePort makes room for the panel on port when another web server holds
// it. It returns an error naming the program it could not move.
func FreePort(port int) error {
	if env.Mock {
		return nil
	}
	pid, name := PortHolder(port)
	switch {
	case pid == 0 || pid == selfPID():
		return nil
	case name == "uhttpd":
		if _, err := Guard(); err != nil {
			return err
		}
		if p, n := PortHolder(port); p != 0 && p != selfPID() && n == "uhttpd" {
			// Still there (config edited by hand to something Guard can't
			// parse): stop it; LuCI stays installed.
			env.Run(15*time.Second, "/etc/init.d/uhttpd", "stop")
		}
	case name == "nginx":
		env.Run(15*time.Second, "/etc/init.d/nginx", "stop")
		env.Run(5*time.Second, "/etc/init.d/nginx", "disable")
		env.Logf("web guard: stopped nginx, it held port %d", port)
	default:
		return fmt.Errorf("port %d is held by %s (pid %d)", port, name, pid)
	}
	if p, n := PortHolder(port); p != 0 && p != selfPID() {
		return fmt.Errorf("port %d is still held by %s", port, n)
	}
	return nil
}

// PortHolderName is the program listening on a TCP port ("" when free).
func PortHolderName(port int) string {
	_, n := PortHolder(port)
	return n
}

// PortHolder finds the process listening on a TCP port via /proc.
func PortHolder(port int) (int, string) {
	inodes := map[string]bool{}
	for _, f := range []string{"net/tcp", "net/tcp6"} {
		b, err := os.ReadFile(filepath.Join(procDir, f))
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n")[1:] {
			x := strings.Fields(l)
			if len(x) < 10 || x[3] != "0A" { // 0A = LISTEN
				continue
			}
			i := strings.LastIndex(x[1], ":")
			p, err := strconv.ParseInt(x[1][i+1:], 16, 32)
			if err == nil && int(p) == port {
				inodes["socket:["+x[9]+"]"] = true
			}
		}
	}
	if len(inodes) == 0 {
		return 0, ""
	}
	procs, _ := os.ReadDir(procDir)
	for _, d := range procs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		fds, _ := os.ReadDir(filepath.Join(procDir, d.Name(), "fd"))
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(procDir, d.Name(), "fd", fd.Name()))
			if err == nil && inodes[link] {
				comm, _ := os.ReadFile(filepath.Join(procDir, d.Name(), "comm"))
				return pid, strings.TrimSpace(string(comm))
			}
		}
	}
	return -1, "unknown"
}

func pidOf(name string) bool {
	_, err := env.Run(3*time.Second, "pidof", name)
	return err == nil
}

func bootEnabled(service string) bool {
	_, err := env.Run(5*time.Second, "/etc/init.d/"+service, "enabled")
	return err == nil
}
