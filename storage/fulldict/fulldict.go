// Package fulldict embeds the gse Chinese dictionary (simplified +
// traditional, ~+31MB binary) so CJK word segmentation works with zero
// downloads and zero build flags. Import for side effects:
//
//	import _ "github.com/ProjAnvil/LadyM/storage/fulldict"
//
// This is the import-path alternative to building with -tags fulldict:
// the dictionary data is linked in only when this package is imported, so
// binaries (including LadyM's own default builds) that do not import it
// stay small. Importing both this package and the build tag is harmless —
// they register the same dictionary.
//
// A downloaded file dictionary (~/.ladyM/dict) still takes precedence over
// this embedded one, so dictionary upgrades remain possible without a
// rebuild.
package fulldict

import (
	"github.com/ProjAnvil/LadyM/storage"
	"github.com/go-ego/gse"
)

func init() {
	storage.SetEmbeddedCJKDict(newSegmenter)
}

// newSegmenter loads gse's embedded Chinese dictionary (simplified +
// traditional). Returned nil on failure so tokenization degrades to the
// per-character fallback instead of erroring.
func newSegmenter() *gse.Segmenter {
	seg, err := gse.NewEmbed("zh")
	if err != nil {
		return nil
	}
	seg.SkipLog = true
	return &seg
}
