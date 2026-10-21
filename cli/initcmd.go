package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/spf13/cobra"
)

// gitignoreBlock is the two-line entry `ladym init` appends so the project's
// runtime memory data never lands in version control.
const gitignoreBlock = "# ladyM\n.ladyM/\n"

// initCmd is `ladym init`: create the memory database (default .ladyM/ladym.db),
// migrate a legacy flat ./ladym.db, and keep .ladyM/ out of git.
func initCmd() *cobra.Command {
	var db string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create the .ladyM/ memory database and update .gitignore.",
		Long: "Create the project-local memory database (default .ladyM/ladym.db).\n" +
			"If a legacy flat ./ladym.db exists it is migrated into .ladyM/.\n" +
			"Appends .ladyM/ to .gitignore when one exists (or when the project is a\n" +
			"git repo). With an explicit --db path only the database is created.",
		RunE: func(c *cobra.Command, args []string) error {
			return runInit(db)
		},
	}
	cmd.Flags().StringVar(&db, "db", "", "Path to ladym.db (skips migration and .gitignore handling)")
	return cmd
}

// runInit implements `ladym init`. Stop serve/worker before migrating: moving
// the db under a live writer leaves the moved file half-written.
func runInit(db string) error {
	cfg, err := loadConfig(db, "")
	if err != nil {
		return err
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	legacy := filepath.Join(wd, "ladym.db")
	target := filepath.Join(wd, ".ladyM", "ladym.db")
	// The default layout covers both the fresh .ladyM/ path and the legacy
	// flat path (defaultDBPath falls back to it while the old file exists).
	defaultLayout := cfg.DBPath == legacy || cfg.DBPath == target

	migrated := false
	if defaultLayout {
		migrated, err = migrateLegacyDB(legacy, target)
		if err != nil {
			return err
		}
		if cfg.DBPath == legacy {
			cfg.DBPath = target // open the moved file, not a recreated legacy one
		}
	}

	// Create the schema (CREATE TABLE IF NOT EXISTS — safe to re-run).
	eng, err := engine.New(cfg)
	if err != nil {
		return err
	}
	if err := eng.Close(); err != nil {
		return err
	}

	if !defaultLayout {
		fmt.Printf("initialized memory database at %s\n", cfg.DBPath)
		if rel, relErr := filepath.Rel(wd, cfg.DBPath); relErr == nil && !strings.HasPrefix(rel, "..") {
			fmt.Println("hint: add this path to .gitignore yourself")
		}
		return nil
	}

	if migrated {
		fmt.Printf("migrated %s -> %s\n", legacy, target)
	}
	fmt.Printf("initialized memory database at %s\n", target)

	switch action, gErr := ensureGitignoreEntry(wd); action {
	case "appended":
		if gErr != nil {
			return gErr
		}
		fmt.Println("appended .ladyM/ to .gitignore")
	case "created":
		if gErr != nil {
			return gErr
		}
		fmt.Println("created .gitignore with .ladyM/")
	case "already":
		// nothing to report
	case "skipped":
		fmt.Println("no .gitignore and no git repo: add .ladyM/ to .gitignore yourself")
	}
	return nil
}

// migrateLegacyDB moves a pre-0.9 flat ./ladym.db (plus -wal/-shm sidecars)
// into .ladyM/. The stale <db>.index.lock is left behind on purpose: it is
// regenerated next to the new path and carries no state worth keeping.
func migrateLegacyDB(legacy, target string) (bool, error) {
	if !pathExists(legacy) {
		return false, nil
	}
	if pathExists(target) {
		return false, &config.ConfigError{Msg: fmt.Sprintf(
			"both %s (legacy) and %s exist: move or delete one of them, then re-run ladym init", legacy, target)}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return false, err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		src := legacy + suffix
		if pathExists(src) {
			if err := os.Rename(src, target+suffix); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

// ensureGitignoreEntry keeps .ladyM/ out of git. Returns the action taken:
// "appended", "created", "already" (entry present), or "skipped" (no
// .gitignore and not a git repo — nothing written).
func ensureGitignoreEntry(wd string) (string, error) {
	path := filepath.Join(wd, ".gitignore")
	if pathExists(path) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "appended", err
		}
		if gitignoreIgnoresLadyM(string(data)) {
			return "already", nil
		}
		content := string(data)
		if len(content) > 0 && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		if err := os.WriteFile(path, []byte(content+"\n"+gitignoreBlock), 0o644); err != nil {
			return "appended", err
		}
		return "appended", nil
	}
	if pathExists(filepath.Join(wd, ".git")) {
		if err := os.WriteFile(path, []byte(gitignoreBlock), 0o644); err != nil {
			return "created", err
		}
		return "created", nil
	}
	return "skipped", nil
}

// gitignoreIgnoresLadyM reports whether a .gitignore body already ignores the
// .ladyM directory (with or without a leading slash or trailing slash).
func gitignoreIgnoresLadyM(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		switch line {
		case ".ladyM", ".ladyM/", "/.ladyM", "/.ladyM/":
			return true
		}
	}
	return false
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
