//go:build !enterprise

package storage

import (
	"strconv"

	"github.com/ProjAnvil/LadyM/config"
)

// OpenStore builds the Store implementation selected by cfg.StoreBackend.
// "sqlite" (the default) is the personal-edition path and behaves exactly as
// before; "postgres" requires a DSN (store.dsn / LADYM_STORE_DSN, or
// store.dsn_env indirection resolved by the config loader).
func OpenStore(cfg *config.Config, dim int) (Store, error) {
	switch cfg.StoreBackend {
	case "", "sqlite":
		return NewStore(cfg.DBPath, dim, cfg.PreferSQLiteVec, cfg.EnableWAL)
	case "postgres":
		if cfg.StoreDSN == "" {
			return nil, &config.ConfigError{Msg: "store.backend = \"postgres\" requires a DSN: " +
				"set store.dsn (or store.dsn_env) in ladym.toml, or export LADYM_STORE_DSN"}
		}
		return NewPostgresStore(cfg.StoreDSN, dim)
	default:
		return nil, &config.ConfigError{Msg: "unknown store.backend " + strconv.Quote(cfg.StoreBackend) +
			": valid values are \"sqlite\" (default) and \"postgres\""}
	}
}
