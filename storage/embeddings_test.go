package storage

import (
	"math"
	"reflect"
	"testing"
)

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"getUserName verify_password authFlow2", []string{"get", "user", "name", "verify", "password", "auth", "flow", "2"}},
		{"Foo-Bar_Baz, x.y; f() {}", []string{"foo", "bar", "baz", ",", "x", ".", "y", ";", "f", "(", ")", "{", "}"}},
		{"get_user_name", []string{"get", "user", "name"}},
		{"HTTPRequest", []string{"http", "request"}},
	}
	for _, c := range cases {
		got := Tokenize(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Tokenize(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestHashingEmbeddingGolden(t *testing.T) {
	// Golden values captured from the Python HashingEmbedding implementation.
	got, err := NewHashingEmbedding(8).Embed("how does authentication work")
	if err != nil {
		t.Fatal(err)
	}
	want := []float32{0.5547001962252291, 0, -0.2773500981126146, 0, 0.5547001962252291, 0, 0, 0.5547001962252291}
	assertVecClose(t, got, want, 1e-6)

	got2, err := NewHashingEmbedding(16).Embed("auth uses JWT with 24h expiry")
	if err != nil {
		t.Fatal(err)
	}
	want2 := []float32{0.3333333333333333, 0, 0.3333333333333333, 0, 0, 0, 0.3333333333333333, 0, 0, 0, -0.3333333333333333, 0, -0.3333333333333333, 0.6666666666666666, 0, 0}
	assertVecClose(t, got2, want2, 1e-6)
}

func assertVecClose(t *testing.T, got, want []float32, tol float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %d want %d", len(got), len(want))
	}
	for i := range got {
		if math.Abs(float64(got[i]-want[i])) > tol {
			t.Fatalf("vec[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestCosineSimilarity(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	if got := CosineSimilarity(a, b); math.Abs(got) > 1e-9 {
		t.Errorf("orthogonal cosine = %v, want 0", got)
	}
	if got := CosineSimilarity(a, a); math.Abs(got-1) > 1e-9 {
		t.Errorf("self cosine = %v, want 1", got)
	}
	// zero vector
	if got := CosineSimilarity([]float32{0, 0}, []float32{1, 1}); got != 0 {
		t.Errorf("zero-vector cosine = %v, want 0", got)
	}
	// dim mismatch
	if got := CosineSimilarity([]float32{1}, []float32{1, 1}); got != 0 {
		t.Errorf("dim-mismatch cosine = %v, want 0", got)
	}
}
