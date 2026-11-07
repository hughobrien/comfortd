// SPDX-License-Identifier: GPL-3.0-or-later

// Command comfortd bridges Mitsubishi Comfort (Kumo Cloud) air conditioners.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hughobrien/comfortd/internal/annotate"
	"github.com/hughobrien/comfortd/internal/history"
	"github.com/hughobrien/comfortd/internal/poller"
	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

var version = "dev"

// discoverRetry is how long to wait between failed discovery attempts.
var discoverRetry = 30 * time.Second

func envOr(flagName, def string) string {
	key := "COMFORTD_" + upperSnake(flagName)
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func upperSnake(s string) string {
	b := []byte(s)
	for i, c := range b {
		switch {
		case c == '-':
			b[i] = '_'
		case c >= 'a' && c <= 'z':
			b[i] = c - 'a' + 'A'
		}
	}
	return string(b)
}

func run(args []string, stdout io.Writer) int {
	return runContext(context.Background(), args, stdout, nil)
}

func runContext(ctx context.Context, args []string, stdout io.Writer, ready func(addr net.Addr)) int {
	fs := flag.NewFlagSet("comfortd", flag.ContinueOnError)
	showVersion := fs.Bool("version", false, "print version and exit")
	listen := fs.String("listen", envOr("listen", "127.0.0.1:9879"), "HTTP listen address")
	stateDir := fs.String("state-dir", envOr("state-dir", "/var/lib/comfortd"), "state directory")
	creds := fs.String("credentials", envOr("credentials", filepath.Join(os.Getenv("CREDENTIALS_DIRECTORY"), "credentials")), "credentials file (email, then password)")
	apiBase := fs.String("api-base", envOr("api-base", kumo.DefaultBaseURL), "Kumo API base URL")
	appVersion := fs.String("app-version", envOr("app-version", kumo.DefaultAppVersion), "x-app-version header")
	interval := fs.Duration("poll-interval", 60*time.Second, "poll interval")
	fake := fs.Bool("fake", false, "run against an in-process fake Kumo server")
	if v := os.Getenv("COMFORTD_POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			*interval = d
		}
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}

	if err := os.MkdirAll(*stateDir, 0o700); err != nil {
		slog.Error("state dir", "err", err)
		return 1
	}
	if *fake {
		srv := kumotest.New()
		defer srv.Close()
		*apiBase = srv.URL
		*creds = filepath.Join(*stateDir, "fake-credentials")
		if err := os.WriteFile(*creds, []byte(kumotest.User+"\n"+kumotest.Password+"\n"), 0o600); err != nil {
			slog.Error("fake credentials", "err", err)
			return 1
		}
	}

	client := kumo.New(kumo.Options{
		BaseURL:         *apiBase,
		AppVersion:      *appVersion,
		CredentialsPath: *creds,
		TokenPath:       filepath.Join(*stateDir, "tokens.json"),
	})

	var units []store.Unit
	for {
		var err error
		units, err = poller.Discover(ctx, client, filepath.Join(*stateDir, "discovery.json"))
		if err == nil {
			break
		}
		slog.Error("discovery failed", "err", err)
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(discoverRetry):
		}
	}

	st := store.New(units)
	hist, err := history.Open(filepath.Join(*stateDir, "history.jsonl"), history.DefaultMaxBytes)
	if err != nil {
		slog.Error("history", "err", err)
		return 1
	}
	defer hist.Close()
	st.Subscribe(func(u store.Update) {
		if err := recordUpdate(hist, u); err != nil {
			slog.Warn("record history", "unit", u.Slug, "endpoint", u.Endpoint, "err", err)
		}
	})
	ann, err := annotate.Load()
	if err != nil {
		slog.Error("annotations", "err", err)
		return 1
	}
	hub := newPushHub()
	st.Subscribe(func(u store.Update) { hub.Publish(pushEvent{Slug: u.Slug}) })
	hist.Subscribe(func(r history.Record) { hub.Publish(pushEvent{Record: &r}) })
	p := poller.New(client, st, *interval)
	go p.Run(ctx)

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		slog.Error("listen", "err", err)
		return 1
	}
	httpSrv := &http.Server{
		Handler:           newMux(deps{Store: st, History: hist, Ann: ann, Hub: hub, Interval: *interval, Now: time.Now}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	slog.Info("listening", "addr", ln.Addr().String())
	if ready != nil {
		ready(ln.Addr())
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpSrv.Shutdown(sctx)
	}()
	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("serve", "err", err)
		return 1
	}
	return 0
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runContext(ctx, os.Args[1:], os.Stdout, nil))
}
