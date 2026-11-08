//go:build !enterprise

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
)

func TestMigrationCLIRecoveryEntry(t *testing.T) {
	dir := t.TempDir()
	cfg := config.ForTesting(dir)
	cfg.EmbeddingCJKTokenizer = "chars"
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	m.ID, m.Content = "fact", "fact"
	if err := eng.Store.PutMemory(m, make([]float32, eng.Provider.Dim())); err != nil {
		t.Fatal(err)
	}
	eng.Close()
	file := filepath.Join(dir, "migrate.toml")
	if err := os.WriteFile(file, []byte("embedding_dim=16\n[embedding]\nprovider='hashing'\ncjk_tokenizer='chars'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := globalConfigPath
	t.Cleanup(func() { globalConfigPath = previous })
	for _, action := range []string{"--status", "--resume", "--status", "--rollback", "--resume", "--finalize"} {
		root := newRootCmd()
		var output bytes.Buffer
		root.SetOut(&output)
		root.SetArgs([]string{"--config", file, "migrate", "embeddings", "--db", cfg.DBPath, action})
		if err := root.Execute(); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if output.Len() == 0 {
			t.Fatal("migration action returned no status")
		}
	}
	root := newRootCmd()
	root.SetArgs([]string{"migrate", "embeddings", "--status", "--resume"})
	if err := root.Execute(); err == nil {
		t.Fatal("multiple migration controls accepted")
	}
}
