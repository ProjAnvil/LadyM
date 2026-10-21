//go:build eval

package eval

import "github.com/tiktoken-go/tokenizer"

// Encoding names a tokenizer scheme understood by the underlying codec.
type Encoding = tokenizer.Encoding

// Cl100kBase is the GPT-4-era chat encoding. This harness uses it purely as a
// reference scheme: counts are comparable across fixtures and scales, but they
// are not billing-accurate figures for any specific vendor.
const Cl100kBase = tokenizer.Cl100kBase

// Counter counts tokens with a fixed codec.
type Counter struct {
	codec tokenizer.Codec
}

// NewCounter loads the codec for enc. The vocabulary ships as compiled Go
// source inside the dependency, so this never touches the network.
func NewCounter(enc Encoding) (*Counter, error) {
	codec, err := tokenizer.Get(enc)
	if err != nil {
		return nil, err
	}
	return &Counter{codec: codec}, nil
}

// Count returns the token count of s. It reports 0 when encoding fails,
// because token counting must never abort a measurement run.
func (c *Counter) Count(s string) int {
	if c == nil || s == "" {
		return 0
	}
	n, err := c.codec.Count(s)
	if err != nil {
		return 0
	}
	return n
}
