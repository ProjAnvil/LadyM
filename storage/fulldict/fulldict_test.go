package fulldict_test

import (
	"testing"

	"github.com/ProjAnvil/LadyM/storage"
	_ "github.com/ProjAnvil/LadyM/storage/fulldict" // registers the embedded dict
)

// TestImportEmbedsDict: with no file dictionary in play, merely importing
// the fulldict package must make an embedded dictionary active. Loads the
// real ~8.7MB dictionary once (~0.5s); still fully offline.
func TestImportEmbedsDict(t *testing.T) {
	storage.SetCJKDictDir(t.TempDir()) // empty: no file dict
	st := storage.CJKDictStatusNow()
	if !st.Available || st.Source != "embedded" {
		t.Fatalf("status = %+v, want source=embedded (registered via import)", st)
	}
	if got := storage.Tokenize("数据库连接池耗尽"); len(got) == 0 {
		t.Fatal("Tokenize returned empty tokens with the embedded dict")
	}
}
