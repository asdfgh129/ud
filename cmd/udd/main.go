// udd is the Universal Downloader daemon (single binary, CGO_ENABLED=0).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/asdfgh129/ud/internal/app"
	"github.com/asdfgh129/ud/internal/version"
)

func main() {
	var opts app.Options
	showVersion := flag.Bool("version", false, "print build version and exit")
	flag.StringVar(&opts.ConfigPath, "config", "", "path to config.yaml")
	flag.StringVar(&opts.Listen, "listen", "", "override listen address, e.g. 127.0.0.1:0")
	flag.StringVar(&opts.Dir, "dir", "", "override download directory")
	flag.StringVar(&opts.DBPath, "db", "", "override SQLite database path")
	flag.StringVar(&opts.LogLevel, "log-level", "", "debug|info|warn|error")
	flag.BoolVar(&opts.InsecurePlugins, "insecure-plugins", false,
		"skip plugin rule-pack signature verification (local development only)")
	flag.Parse()
	if *showVersion {
		fmt.Println("udd", version.String())
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, opts); err != nil {
		fmt.Fprintln(os.Stderr, "udd:", err)
		os.Exit(1)
	}
}
