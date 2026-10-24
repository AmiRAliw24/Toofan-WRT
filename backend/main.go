package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"separ/internal/api"
	"separ/internal/config"
	"separ/internal/core"
	"separ/internal/env"
)

//go:embed all:web/dist
var webFS embed.FS

var version = "dev"

func main() {
	listen := flag.String("listen", ":8080", "HTTP listen address")
	cfgPath := flag.String("config", "/etc/separ/config.json", "config file")
	xray := flag.String("xray", "/usr/bin/xray", "xray binary")
	webDir := flag.String("web", "", "serve the UI from this directory instead of the embedded copy")
	mock := flag.Bool("mock", false, "simulate a router (for development)")
	flag.Parse()

	env.Mock = *mock
	if env.Mock {
		env.DataDir = os.TempDir() + "/separ-mock"
	}
	store, err := config.Open(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	c := core.New(store, *xray)
	if _, port, err := net.SplitHostPort(*listen); err == nil {
		c.PanelPort, _ = strconv.Atoi(port)
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

	log.Printf("separ %s listening on %s", version, *listen)
	log.Fatal(http.ListenAndServe(*listen, srv.Handler()))
}
