//go:build enterprise

// Enterprise edition: the console role is a SEPARATE BINARY (cmd/ladymconsole)
// — api/worker nodes (`ladym serve --http`, `ladym worker`) do not embed the
// Vue console at all. This file therefore deliberately does NOT import the
// console package: the SPA mount is injected by the caller (cmd/ladymconsole
// passes console.Mount), keeping the cli package — and with it the ladym
// binary — free of the console embed in enterprise builds. The console command
// is NOT registered on the ladym root command in either edition.
package cli

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/ProjAnvil/LadyM/api"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/storage"
	"github.com/spf13/cobra"
)

// NewConsoleCmd builds the console-role command — the root command of the
// standalone ladymconsole binary: the full /api data-plane plus the console
// SPA mounted at "/", against the same Postgres deployment as the api nodes
// (identical config: store.backend/dsn, auth.enabled, ...). mount registers
// the SPA on the mux (cmd/ladymconsole passes console.Mount).
func NewConsoleCmd(mount func(*http.ServeMux)) *cobra.Command {
	var db, workspace, httpAddr string
	cmd := &cobra.Command{
		Use:   "ladymconsole",
		Short: "Serve the LadyM management console (full /api data-plane + SPA) over HTTP.",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			cfg, err := loadConfig(db, workspace)
			if err != nil {
				return err
			}
			return serveConsoleHTTP(cfg, httpAddr, mount)
		},
	}
	addDBWS(cmd, &db, &workspace)
	cmd.Flags().StringVar(&httpAddr, "http", ":8080", "Listen address (e.g. :8080 or 8080)")
	cmd.PersistentFlags().StringVar(&globalConfigPath, "config", "", "Path to a ladym.toml to load on top of defaults/env.")
	cmd.PersistentFlags().BoolVar(&globalDebug, "debug", false, "Show full error details on error.")
	return cmd
}

// ExecuteConsole is the ladymconsole binary's entry point (cmd/ladymconsole):
// same error handling as Execute.
func ExecuteConsole(mount func(*http.ServeMux)) {
	cmd := NewConsoleCmd(mount)
	cmd.Version = storage.Edition
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	if err := cmd.Execute(); err != nil {
		fatalOnError(err)
	}
}

// buildConsoleHTTPHandler constructs the console-role handler: the api
// data-plane mux (api.NewMux) plus the console SPA at "/" (mounted via the
// injected mount), wrapped in the standard auth + observability middleware —
// the same /api capabilities as api.NewHandler. Split from serveConsoleHTTP so
// tests exercise construction without binding a port. The store is returned
// alongside for the startup banner's auth summary.
func buildConsoleHTTPHandler(cfg *config.Config, mount func(*http.ServeMux)) (http.Handler, storage.Store, func() error, error) {
	eng, err := engine.New(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	mux, wrap := api.NewMux(eng, cfg)
	mount(mux)
	return wrap(mux), eng.Store, eng.Close, nil
}

// consoleHTTPBanner mirrors serveHTTPBanner, labelled as the console role:
// listen address, store backend, workspace and the auth mode.
func consoleHTTPBanner(cfg *config.Config, addr string, store storage.Store) string {
	s := "backend=" + cfg.StoreBackend
	if cfg.StoreBackend != "postgres" {
		s += fmt.Sprintf(", db=%s", cfg.DBPath)
	}
	return fmt.Sprintf("LadyM console server listening on %s (%s, ws=%s)\n  auth=%s\n",
		addr, s, cfg.Workspace, api.DescribeAuth(cfg, store))
}

// serveConsoleHTTP runs the console role's HTTP server (banner to stderr, same
// convention as serveHTTP).
func serveConsoleHTTP(cfg *config.Config, addr string, mount func(*http.ServeMux)) error {
	if !strings.Contains(addr, ":") {
		addr = ":" + addr
	}
	h, store, closeFn, err := buildConsoleHTTPHandler(cfg, mount)
	if err != nil {
		return err
	}
	defer closeFn()
	fmt.Fprint(os.Stderr, consoleHTTPBanner(cfg, addr, store))
	return http.ListenAndServe(addr, h)
}
