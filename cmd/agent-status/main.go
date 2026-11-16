// Command agent-status collects status events from coding agents on the LAN
// and serves a read-only page with the state of every session.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"agent-status/internal/status"
	"agent-status/internal/web"
)

func main() {
	addr := flag.String("addr", ":7777", "listen address")
	statePath := flag.String("state", "agent-status.json", "state file (empty disables persistence)")
	logPath := flag.String("log", "", "log file (default: stderr)")
	archiveDir := flag.String("archive", "auto", `directory for the event archive used by statistics ("auto": "archive" next to the state file; empty disables)`)
	flag.Parse()
	if *archiveDir == "auto" {
		*archiveDir = ""
		if *statePath != "" {
			*archiveDir = filepath.Join(filepath.Dir(*statePath), "archive")
		}
	}

	out := os.Stderr
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			slog.Error("open log", "err", err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
	}
	log := slog.New(slog.NewTextHandler(out, nil))
	if err := run(*addr, *statePath, *archiveDir, log); err != nil {
		log.Error("agent-status stopped", "err", err)
		os.Exit(1)
	}
}

func run(addr, statePath, archiveDir string, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	store, err := status.NewStore(statePath)
	if err != nil {
		return err
	}
	var archive *status.Archive
	if archiveDir != "" {
		if archive, err = status.OpenArchive(archiveDir); err != nil {
			return err
		}
		defer archive.Close()
		log.Info("archiving events", "dir", archiveDir)
	}
	// Last heartbeat of every machine, next to the state file.
	machinesPath := ""
	if statePath != "" {
		machinesPath = filepath.Join(filepath.Dir(statePath), "machines.json")
	}
	machines, err := status.NewMachines(machinesPath)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           web.New(store, archive, machines, log).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := store.Save(); err != nil {
					log.Warn("save state", "err", err)
				}
				if err := machines.Save(); err != nil {
					log.Warn("save machines", "err", err)
				}
			}
		}
	}()

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	_ = machines.Save()
	return store.Save()
}
