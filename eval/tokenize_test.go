//go:build eval

package eval

import "testing"

func TestCounterEmptyStringIsZero(t *testing.T) {
	c, err := NewCounter(Cl100kBase)
	if err != nil {
		t.Fatalf("NewCounter: %v", err)
	}
	if got := c.Count(""); got != 0 {
		t.Errorf("Count(\"\") = %d, want 0", got)
	}
}

func TestCounterKnownEnglishPhrase(t *testing.T) {
	c, err := NewCounter(Cl100kBase)
	if err != nil {
		t.Fatalf("NewCounter: %v", err)
	}
	if got := c.Count("hello world"); got != 2 {
		t.Errorf("Count(\"hello world\") = %d, want 2", got)
	}
}

func TestCounterCJKIsNotCharDividedByFour(t *testing.T) {
	c, err := NewCounter(Cl100kBase)
	if err != nil {
		t.Fatalf("NewCounter: %v", err)
	}
	const s = "评测台要量中文落库的真实体量"
	got := c.Count(s)
	if got <= len([]rune(s))/4 {
		t.Errorf("Count(CJK) = %d, want > %d (char/4 underestimates CJK)", got, len([]rune(s))/4)
	}
}

func TestCounterIsMonotonicUnderConcatenation(t *testing.T) {
	c, err := NewCounter(Cl100kBase)
	if err != nil {
		t.Fatalf("NewCounter: %v", err)
	}
	a, b := "first chunk of text", "second chunk of text"
	ab := c.Count(a + b)
	if ab < c.Count(a) || ab < c.Count(b) {
		t.Errorf("Count(%q) = %d, must be >= each part (%d, %d)", a+b, ab, c.Count(a), c.Count(b))
	}
}

func TestCounterIsDeterministic(t *testing.T) {
	c, err := NewCounter(Cl100kBase)
	if err != nil {
		t.Fatalf("NewCounter: %v", err)
	}
	s := "repeat me exactly twice"
	if first, second := c.Count(s), c.Count(s); first != second {
		t.Errorf("Count = %d then %d, want identical", first, second)
	}
}

func TestNewCounterRejectsUnknownEncoding(t *testing.T) {
	if _, err := NewCounter(Encoding("not-a-real-encoding")); err == nil {
		t.Fatal("NewCounter accepted an unknown encoding")
	}
}
