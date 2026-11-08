package storage

import (
	"crypto/sha256"
	"fmt"
	"math"
	"slices"

	"github.com/go-ego/gse"
)

// Reloads replace global segmenters; a provider retains the resolved dictionary
// and script coverage for its entire lifetime, including cache misses.
type hashingTokenizer struct {
	segmenter *gse.Segmenter
	variant   CJKDictName
	scripts   []string
	version   string
}

var hashingTokenizerCache hashingTokenizer // guarded by cjkMu

func captureHashingTokenizer(mode, dir string) hashingTokenizer {
	cjkMu.Lock()
	defer cjkMu.Unlock()
	if mode == "" {
		mode = cjkTokenizerMode
	}
	if mode == CJKTokenizerChars {
		return hashingTokenizer{version: "chars-v1"}
	}
	var seg *gse.Segmenter
	var variant CJKDictName
	global := dir == "" || dir == cjkDictDirFn()
	if !global {
		seg, _, variant = loadCJKDictFrom(dir)
	} else {
		maybeReprobeLocked()
		seg, variant = cjkSeg, cjkVariant
		if hashingTokenizerCache.version != "" && hashingTokenizerCache.segmenter == seg && hashingTokenizerCache.variant == variant {
			return hashingTokenizerCache
		}
	}
	tokenizer := dictionaryHashingTokenizer(seg, variant)
	if global {
		hashingTokenizerCache = tokenizer
	}
	return tokenizer
}

func dictionaryHashingTokenizer(seg *gse.Segmenter, variant CJKDictName) hashingTokenizer {
	tokenizer := hashingTokenizer{segmenter: seg, variant: variant, version: "dict-v2:none"}
	if seg == nil {
		return tokenizer
	}
	tokenizer.scripts = slices.Clone(cjkRegistryFn()[variant].Scripts)
	digest := sha256.New()
	var encoded []byte
	for _, token := range seg.Dictionary().Tokens {
		// Hash the loaded data, not a path or manifest which may have changed.
		encoded = fmt.Appendf(encoded[:0], "%q %x %q\n", token.Text(), math.Float64bits(token.Freq()), token.Pos())
		digest.Write(encoded)
	}
	tokenizer.version = fmt.Sprintf("dict-v2:%s:%x", variant, digest.Sum(nil))
	return tokenizer
}

func (t hashingTokenizer) segment(run string) []string {
	if t.segmenter != nil && slices.Contains(t.scripts, runScript(run)) {
		if words := t.segmenter.Cut(run, true); len(words) > 0 {
			return words
		}
	}
	return perRuneTokens(run)
}
