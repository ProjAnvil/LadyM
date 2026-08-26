//go:build !fulldict

package storage

import "github.com/go-ego/gse"

// newEmbeddedCJKSegmenter returns nil in default builds: gse's embedded
// dictionaries cost ~31MB of binary, so they are only linked in under the
// fulldict tag. Without a downloaded file dict, Han runs tokenize
// per-character via the fallback in segmentCJK.
func newEmbeddedCJKSegmenter() *gse.Segmenter { return nil }
