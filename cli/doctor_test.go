package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
)

func doctorCfg(dbPath string) *config.Config {
	cfg := config.Default()
	cfg.DBPath = dbPath
	return cfg
}

func doctorFind(findings []doctorFinding, name string) doctorFinding {
	for _, f := range findings {
		if f.Name == name {
			return f
		}
	}
	return doctorFinding{Name: "missing:" + name}
}

func TestLegacyHomeDirSplit(t *testing.T) {
	cases := []struct {
		name                             string
		legacyExists, curExists, sameFil bool
		want                             bool
	}{
		{"no legacy dir", false, true, false, false},
		{"same directory", true, true, true, false},
		{"legacy only, home missing", true, false, false, true},
		{"two distinct directories", true, true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := legacyHomeDirSplit(tc.legacyExists, tc.curExists, tc.sameFil); got != tc.want {
				t.Errorf("legacyHomeDirSplit(%v, %v, %v) = %v, want %v",
					tc.legacyExists, tc.curExists, tc.sameFil, got, tc.want)
			}
		})
	}
}

// Both spellings name the same directory on a case-insensitive filesystem, so
// the only portable assertion here is that nothing is reported as an error.
func TestDoctorLegacyHomeDirNotAnErrorWithoutSplit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(config.HomeDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	f := checkLegacyHomeDir(home)
	if f.Status != string(statusOK) {
		t.Errorf("legacy home dir status = %q, want ok (detail %q)", f.Status, f.Detail)
	}
}

func TestDoctorDatabase(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		f := checkDatabase(doctorCfg(filepath.Join(t.TempDir(), "ladym.db")))
		if f.Status != string(statusWarn) {
			t.Errorf("status = %q, want warn", f.Status)
		}
		if !strings.Contains(f.Hint, "ladym init") {
			t.Errorf("hint = %q, want it to name `ladym init`", f.Hint)
		}
	})
	t.Run("present", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "ladym.db")
		if err := os.WriteFile(db, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
		f := checkDatabase(doctorCfg(db))
		if f.Status != string(statusOK) {
			t.Errorf("status = %q, want ok (detail %q)", f.Status, f.Detail)
		}
	})
	t.Run("postgres backend", func(t *testing.T) {
		cfg := doctorCfg("")
		cfg.StoreBackend = "postgres"
		if f := checkDatabase(cfg); f.Status != string(statusOK) {
			t.Errorf("status = %q, want ok (detail %q)", f.Status, f.Detail)
		}
	})
}

func TestDoctorGitignore(t *testing.T) {
	t.Run("not a git repo", func(t *testing.T) {
		f := checkGitignore(t.TempDir())
		if f.Status != string(statusOK) {
			t.Errorf("status = %q, want ok (detail %q)", f.Status, f.Detail)
		}
	})
	t.Run("git repo without gitignore", func(t *testing.T) {
		wd := t.TempDir()
		if err := os.MkdirAll(filepath.Join(wd, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		f := checkGitignore(wd)
		if f.Status != string(statusWarn) {
			t.Errorf("status = %q, want warn (detail %q)", f.Status, f.Detail)
		}
	})
	t.Run("git repo ignoring the dotdir", func(t *testing.T) {
		wd := t.TempDir()
		if err := os.MkdirAll(filepath.Join(wd, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wd, ".gitignore"), []byte("node_modules/\n.ladyM/\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		f := checkGitignore(wd)
		if f.Status != string(statusOK) {
			t.Errorf("status = %q, want ok (detail %q)", f.Status, f.Detail)
		}
	})
}

func TestDoctorLegacyFlatDBWarns(t *testing.T) {
	wd := t.TempDir()
	if err := os.WriteFile(filepath.Join(wd, "ladym.db"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	f := checkLegacyFlatDB(wd)
	if f.Status != string(statusWarn) {
		t.Errorf("status = %q, want warn (detail %q)", f.Status, f.Detail)
	}
	if !strings.Contains(f.Hint, config.DirName) {
		t.Errorf("hint = %q, want it to name %s", f.Hint, config.DirName)
	}
}

func TestDoctorGlobalConfigError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, config.DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("not = [valid"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, loadErr := config.Load("", nil)
	f := checkGlobalConfig(loadErr)
	if f.Status != string(statusError) {
		t.Errorf("status = %q, want error (detail %q)", f.Status, f.Detail)
	}
}

func TestDoctorChecksNoErrorOnCleanInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wd := t.TempDir()
	db := filepath.Join(wd, config.DirName, "ladym.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := doctorChecks(wd, doctorCfg(db), nil)
	for _, f := range findings {
		if f.Status == string(statusError) {
			t.Errorf("check %q reported an error on a clean install: %s", f.Name, f.Detail)
		}
	}
	if len(findings) == 0 {
		t.Fatal("doctorChecks returned no findings")
	}
}

// A doctor that returns non-zero for a merely uninitialized install is useless,
// so only error-level findings may fail the command.
func TestDoctorExitsZeroWithoutErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wd := t.TempDir()
	db := filepath.Join(wd, config.DirName, "ladym.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(wd)
	t.Setenv("LADYM_DB", db)

	out, err := runCmd(t, newRootCmd(), "doctor", "--db", db)
	if err != nil {
		t.Fatalf("doctor exited with %v; output:\n%s", err, out)
	}
	if !strings.Contains(out, "LadyM doctor") {
		t.Errorf("output missing header:\n%s", out)
	}
	if !strings.Contains(out, "dict dir") {
		t.Errorf("output should list the dict dir check:\n%s", out)
	}
}

func TestDoctorJSONOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wd := t.TempDir()
	t.Chdir(wd)
	t.Setenv("LADYM_DB", filepath.Join(wd, config.DirName, "ladym.db"))

	out, err := runCmd(t, newRootCmd(), "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor --json exited with %v; output:\n%s", err, out)
	}
	if !strings.Contains(out, `"name": "home dir"`) {
		t.Errorf("output should be JSON findings:\n%s", out)
	}
}

func TestWriteDoctorStableOrderWithoutMutatingFindings(t *testing.T) {
	findings := []doctorFinding{
		{Name: "ok-first", Status: string(statusOK)},
		{Name: "warn-first", Status: string(statusWarn), Hint: "warning hint"},
		{Name: "error-first", Status: string(statusError)},
		{Name: "warn-second", Status: string(statusWarn)},
		{Name: "error-second", Status: string(statusError)},
		{Name: "ok-second", Status: string(statusOK)},
	}
	original := slices.Clone(findings)
	var out strings.Builder
	writeDoctor(&out, findings)

	var names []string
	for line := range strings.SplitSeq(out.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] != "LadyM" && fields[0] != "->" {
			names = append(names, fields[1])
		}
	}
	want := []string{"error-first", "error-second", "warn-first", "warn-second", "ok-first", "ok-second"}
	if !slices.Equal(names, want) {
		t.Errorf("finding order = %v, want %v", names, want)
	}
	if !slices.Equal(findings, original) {
		t.Errorf("writeDoctor mutated findings: got %v, want %v", findings, original)
	}
	if !strings.Contains(out.String(), "-> warning hint") {
		t.Errorf("output missing hint:\n%s", out.String())
	}
}

func TestWriteDoctorEmptyFindings(t *testing.T) {
	for _, findings := range [][]doctorFinding{nil, {}} {
		var out strings.Builder
		writeDoctor(&out, findings)
		if got := out.String(); got != "LadyM doctor\n\n" {
			t.Errorf("writeDoctor(%v) = %q, want header only", findings, got)
		}
	}
}
