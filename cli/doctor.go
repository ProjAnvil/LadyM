package cli

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/spf13/cobra"
)

// doctorStatus is the severity of one diagnostic. Only statusError makes
// `ladym doctor` exit non-zero: a fresh install legitimately has no database
// and no dictionary, and reporting those as failures would make the command
// useless for the common case.
type doctorStatus string

const (
	statusOK    doctorStatus = "ok"
	statusWarn  doctorStatus = "warn"
	statusError doctorStatus = "error"
)

// doctorFinding is one check result: what was checked, how bad it is, what was
// found and (when it matters) what to do about it.
type doctorFinding struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
}

func doctorCmd() *cobra.Command {
	var db, workspace, dictDir string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Report configuration and storage problems.",
		Long: "Check the LadyM installation and print one line per finding.\n" +
			"Read-only: no LLM calls, no writes, no network.\n" +
			"Exits non-zero only when a finding is an error.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			cfg, loadErr := loadConfigExtra(db, workspace, dictDir)
			if loadErr != nil {
				// Keep diagnosing: fall back to defaults (plus --db) so the
				// remaining checks still run against the paths asked about.
				cfg = config.Default()
				if db != "" {
					cfg.DBPath = db
				}
			}
			wd, err := os.Getwd()
			if err != nil {
				wd = ""
			}
			findings := doctorChecks(wd, cfg, loadErr)
			out := c.OutOrStdout()
			if jsonOut {
				return writeDoctorJSON(out, findings)
			}
			writeDoctor(out, findings)
			for _, f := range findings {
				if f.Status == string(statusError) {
					return &exitError{code: 1}
				}
			}
			return nil
		},
	}
	addDBWS(cmd, &db, &workspace)
	addDictDirFlag(cmd, &dictDir)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit JSON.")
	return cmd
}

// doctorChecks runs every diagnostic. wd is passed in so tests can point it at
// a temp directory; loadErr is the error from resolving the config, reported by
// the global-config check.
func doctorChecks(wd string, cfg *config.Config, loadErr error) []doctorFinding {
	userHome, err := os.UserHomeDir()
	if err != nil {
		userHome = "."
	}
	return []doctorFinding{
		checkHomeDir(config.HomeDir()),
		checkLegacyHomeDir(userHome),
		checkGlobalConfig(loadErr),
		checkDatabase(cfg),
		checkLegacyFlatDB(wd),
		checkGitignore(wd),
		checkDictDir(cfg.CJKDictDir),
	}
}

func checkHomeDir(home string) doctorFinding {
	if dirExists(home) {
		return doctorFinding{Name: "home dir", Status: string(statusOK), Detail: home}
	}
	return doctorFinding{
		Name:   "home dir",
		Status: string(statusOK),
		Detail: home + " not created yet",
		Hint:   "created on first use (secrets store, dictionary download)",
	}
}

// checkLegacyHomeDir detects the case split: on a case-sensitive filesystem
// ~/.ladym and ~/.ladyM are two directories, and everything under the legacy
// one — the global config above all — is invisible to LadyM.
// checkLegacyHomeDir takes the user's home, not config.HomeDir: it resolves
// both spellings from it to see whether they land on the same directory.
func checkLegacyHomeDir(userHome string) doctorFinding {
	cur := filepath.Join(userHome, config.DirName)
	legacy := filepath.Join(userHome, config.LegacyDirName)
	legacyInfo, legacyErr := os.Stat(legacy)
	curInfo, curErr := os.Stat(cur)
	same := legacyErr == nil && curErr == nil && os.SameFile(curInfo, legacyInfo)
	if !legacyHomeDirSplit(legacyErr == nil, curErr == nil, same) {
		if legacyErr == nil {
			return doctorFinding{
				Name:   "legacy home dir",
				Status: string(statusOK),
				Detail: "same directory as " + cur + " (case-insensitive filesystem)",
			}
		}
		return doctorFinding{Name: "legacy home dir", Status: string(statusOK), Detail: "none"}
	}
	return doctorFinding{
		Name:   "legacy home dir",
		Status: string(statusError),
		Detail: legacy + " is a separate directory; LadyM does not read it",
		Hint:   "move its files to " + cur + " (config.toml, dict, secrets)",
	}
}

// legacyHomeDirSplit is the pure decision behind checkLegacyHomeDir. The three
// inputs are injected because a case-insensitive filesystem cannot produce two
// distinct directories named ~/.ladym and ~/.ladyM, so the split branch is
// unreachable from disk state alone on macOS.
func legacyHomeDirSplit(legacyExists, curExists, sameFile bool) bool {
	return legacyExists && (!curExists || !sameFile)
}

func checkGlobalConfig(loadErr error) doctorFinding {
	path := config.GlobalConfigPath()
	if path == "" {
		return doctorFinding{Name: "global config", Status: string(statusOK), Detail: "none, using defaults"}
	}
	if loadErr != nil {
		return doctorFinding{
			Name:   "global config",
			Status: string(statusError),
			Detail: path + ": " + loadErr.Error(),
			Hint:   "fix or remove the file",
		}
	}
	return doctorFinding{Name: "global config", Status: string(statusOK), Detail: path}
}

func checkDatabase(cfg *config.Config) doctorFinding {
	if cfg.StoreBackend == "postgres" {
		return doctorFinding{Name: "database", Status: string(statusOK), Detail: "postgres backend, no local db file"}
	}
	if cfg.DBPath != "" && fileOrDirExists(cfg.DBPath) {
		return doctorFinding{Name: "database", Status: string(statusOK), Detail: cfg.DBPath}
	}
	return doctorFinding{
		Name:   "database",
		Status: string(statusWarn),
		Detail: cfg.DBPath + " not found",
		Hint:   "run `ladym init`",
	}
}

// checkLegacyFlatDB flags the pre-0.9 layout: a flat ./ladym.db in the project
// root, which `ladym init` migrates into the dotdir.
func checkLegacyFlatDB(wd string) doctorFinding {
	if wd == "" {
		return doctorFinding{Name: "legacy flat db", Status: string(statusOK), Detail: "none"}
	}
	flat := filepath.Join(wd, "ladym.db")
	if !fileOrDirExists(flat) {
		return doctorFinding{Name: "legacy flat db", Status: string(statusOK), Detail: "none"}
	}
	return doctorFinding{
		Name:   "legacy flat db",
		Status: string(statusWarn),
		Detail: flat + " still in the project root",
		Hint:   "run `ladym init` to migrate it into " + config.DirName + "/",
	}
}

func checkGitignore(wd string) doctorFinding {
	if wd == "" || !dirExists(filepath.Join(wd, ".git")) {
		return doctorFinding{Name: "gitignore", Status: string(statusOK), Detail: "not a git repo"}
	}
	body, err := os.ReadFile(filepath.Join(wd, ".gitignore"))
	if err != nil {
		return doctorFinding{
			Name:   "gitignore",
			Status: string(statusWarn),
			Detail: "no .gitignore in the repo root",
			Hint:   "add " + config.DirName + "/ so the memory db stays out of git",
		}
	}
	if gitignoreIgnoresLadyM(string(body)) {
		return doctorFinding{Name: "gitignore", Status: string(statusOK), Detail: "ignores " + config.DirName + "/"}
	}
	return doctorFinding{
		Name:   "gitignore",
		Status: string(statusWarn),
		Detail: "does not ignore " + config.DirName + "/",
		Hint:   "add " + config.DirName + "/ so the memory db stays out of git",
	}
}

func checkDictDir(cfgDir string) doctorFinding {
	dir := cfgDir
	if dir == "" {
		dir = filepath.Join(config.HomeDir(), "dict")
	}
	if dirExists(dir) {
		return doctorFinding{Name: "dict dir", Status: string(statusOK), Detail: dir}
	}
	return doctorFinding{
		Name:   "dict dir",
		Status: string(statusWarn),
		Detail: dir + " not found",
		Hint:   "CJK text falls back to per-character tokens; download the dictionary in the console",
	}
}

// writeDoctor prints the findings as an aligned two-column report, sorted so
// errors and warnings surface above the passing checks.
func writeDoctor(w io.Writer, findings []doctorFinding) {
	fmt.Fprintln(w, "LadyM doctor")
	fmt.Fprintln(w)
	ordered := slices.Clone(findings)
	slices.SortStableFunc(ordered, func(a, b doctorFinding) int {
		return cmp.Compare(doctorSeverity(b), doctorSeverity(a))
	})
	for _, f := range ordered {
		fmt.Fprintf(w, "  %-5s %-17s %s\n", f.Status, f.Name, f.Detail)
		if f.Hint != "" {
			fmt.Fprintf(w, "  %-5s %-17s -> %s\n", "", "", f.Hint)
		}
	}
}

func doctorSeverity(f doctorFinding) int {
	switch f.Status {
	case string(statusError):
		return 3
	case string(statusWarn):
		return 2
	default:
		return 1
	}
}

func writeDoctorJSON(w io.Writer, findings []doctorFinding) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(findings)
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func fileOrDirExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
