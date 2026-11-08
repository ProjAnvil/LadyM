package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveConfig(t *testing.T) {
	cfg := Default()
	if cfg.Archive.Enabled || cfg.Archive.MaxEntryBytes != 65536 || cfg.Archive.MaxWorkspaceBytes != 8388608 || cfg.Archive.RetentionS != 2592000 {
		t.Fatalf("defaults = %+v", cfg.Archive)
	}
	for _, tc := range []struct {
		text  string
		valid bool
	}{
		{"enabled=true\nmax_entry_bytes=24\nmax_workspace_bytes=48\nretention_s=2.5", true},
		{"max_entry_bytes=0", false}, {"max_workspace_bytes=-1", false},
		{"max_entry_bytes=1.5", false}, {"retention_s=nan", false},
		{"retention_s=inf", false}, {"retention_s=0", false},
		{`enabled="true"`, false}, {`max_entry_bytes="bad"`, false},
	} {
		path := filepath.Join(t.TempDir(), "archive.toml")
		if err := os.WriteFile(path, []byte("[archive]\n"+tc.text+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := FromFile(path)
		if (err == nil) != tc.valid {
			t.Fatalf("%s: config=%+v err=%v", tc.text, got, err)
		}
	}
}
