package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"toofanwrt/internal/api"
	"toofanwrt/internal/config"
	"toofanwrt/internal/core"
	"toofanwrt/internal/env"
)

//go:embed all:web/dist
var webFS embed.FS

var version = "dev"

func main() {
	listen := flag.String("listen", ":8080", "HTTP listen addresses, comma separated")
	cfgPath := flag.String("config", "/etc/toofanwrt/config.json", "config file")
	xray := flag.String("xray", "/usr/bin/xray", "xray binary")
	webDir := flag.String("web", "", "serve the UI from this directory instead of the embedded copy")
	mock := flag.Bool("mock", false, "simulate a router (for development)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	env.Mock = *mock
	if env.Mock {
		env.DataDir = os.TempDir() + "/toofanwrt-mock"
	}
	store, err := config.Open(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	c := core.New(store, *xray)
	addrs := strings.Split(*listen, ",")
	for _, a := range addrs {
		if _, port, err := net.SplitHostPort(strings.TrimSpace(a)); err == nil {
			if p, _ := strconv.Atoi(port); p > 0 {
				c.PanelPorts = append(c.PanelPorts, p)
			}
		}
	}
	c.Start()

	var web fs.FS
	if *webDir != "" {
		web = os.DirFS(*webDir)
	} else {
		web, _ = fs.Sub(webFS, "web/dist")
	}
	srv := api.New(c, web, version)

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
		for s := range sig {
			if s == syscall.SIGHUP {
				// Sent by the hotplug script when the WAN comes up.
				c.Reapply()
				c.Proxy.Kick()
				continue
			}
			log.Printf("shutting down")
			c.Shutdown()
			os.Exit(0)
		}
	}()

	log.Printf("toofanwrt %s listening on %s", version, *listen)
	h := srv.Handler()
	// A busy port (e.g. LuCI still holding :80) must not take the panel
	// down: keep retrying it while the other ports serve.
	for _, a := range addrs {
		go func(a string) {
			for {
				err := http.ListenAndServe(strings.TrimSpace(a), h)
				log.Printf("listen %s: %v (retrying)", a, err)
				time.Sleep(5 * time.Second)
			}
		}(a)
	}
	select {}
}
