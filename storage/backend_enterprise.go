//go:build enterprise

package storage

import (
	"strconv"

	"github.com/ProjAnvil/LadyM/config"
)

// OpenStore builds the Store implementation selected by cfg.StoreBackend.
// This is the enterprise-edition variant: the binary is compiled without the
// SQLite backend, so "postgres" is the only valid backend and requires a DSN
// (store.dsn / LADYM_STORE_DSN, or store.dsn_env indirection resolved by the
// config loader). "sqlite" — including the empty default that falls back to
// sqlite — fails fast with an actionable config error.
func OpenStore(cfg *config.Config, dim int) (Store, error) {
	switch cfg.StoreBackend {
	case "postgres":
		if cfg.StoreDSN == "" {
			return nil, &config.ConfigError{Msg: "store.backend = \"postgres\" requires a DSN: " +
				"set store.dsn (or store.dsn_env) in ladym.toml, or export LADYM_STORE_DSN"}
		}
		return NewPostgresStore(cfg.StoreDSN, dim)
	case "", "sqlite":
		return nil, &config.ConfigError{Msg: "this is an enterprise build without the SQLite backend: " +
			"set store.backend = \"postgres\" and store.dsn in ladym.toml " +
			"(or export LADYM_STORE_BACKEND=postgres and LADYM_STORE_DSN)"}
	default:
		return nil, &config.ConfigError{Msg: "unknown store.backend " + strconv.Quote(cfg.StoreBackend) +
			": the only valid value in this enterprise build is \"postgres\""}
	}
}
