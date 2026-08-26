//go:build fulldict

package storage

import "github.com/go-ego/gse"

// newEmbeddedCJKSegmenter loads gse's embedded Chinese dictionary
// (simplified + traditional). Builds with -tags fulldict carry the
// dictionaries in the binary (~+31MB) so CJK word segmentation works with
// zero downloads — for offline or appliance-style deployments.
func newEmbeddedCJKSegmenter() *gse.Segmenter {
	seg, err := gse.NewEmbed("zh")
	if err != nil {
		return nil
	}
	seg.SkipLog = true
	return &seg
}
