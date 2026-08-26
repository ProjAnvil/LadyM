package storage

import (
	"math"
	"reflect"
	"strings"
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
		// Python _TOKEN_RE includes ':' in the punctuation set, so "key: value"
		// yields a standalone ":" token.
		{"key: value", []string{"key", ":", "value"}},
	}
	for _, c := range cases {
		got := Tokenize(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Tokenize(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestTokenizeCJK(t *testing.T) {
	installTestDict(t)
	cases := []struct {
		in   string
		want []string
	}{
		// gse jieba dictionary segmentation for Han runs.
		{"用户登录失败", []string{"用户", "登录", "失败"}},
		{"数据库连接池耗尽", []string{"数据库", "连接池", "耗尽"}},
		// Mixed ASCII + CJK: both scripts keep their tokens.
		{"error in 用户登录 module", []string{"error", "in", "用户", "登录", "module"}},
		// Kana and hangul have no embedded dictionary: per-character tokens.
		{"テスト", []string{"テ", "ス", "ト"}},
		{"しました", []string{"し", "ま", "し", "た"}},
		{"안녕하세요", []string{"안", "녕", "하", "세", "요"}},
		// Fullwidth punctuation is outside the punctuation set and is dropped,
		// matching how unknown ASCII punctuation behaves.
		{"你好，世界", []string{"你好", "世界"}},
	}
	for _, c := range cases {
		got := Tokenize(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Tokenize(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	// Traditional Chinese segments via the embedded zh_t dictionary; assert
	// structure rather than exact words to stay robust against dictionary
	// updates.
	trad := Tokenize("資料庫連線逾時")
	if len(trad) == 0 {
		t.Errorf("Tokenize(traditional Chinese) = empty, want non-empty tokens")
	}
	joined := strings.Join(trad, "")
	for _, r := range "資料庫連線逾時" {
		if !strings.ContainsRune(joined, r) {
			t.Errorf("traditional tokens %v lost rune %q", trad, r)
		}
	}
}

func TestHashingEmbeddingCJK(t *testing.T) {
	installTestDict(t)
	h := NewHashingEmbedding(256)
	zh := "用户登录失败"
	v, err := h.Embed(zh)
	if err != nil {
		t.Fatal(err)
	}
	nonzero := 0
	for _, x := range v {
		if x != 0 {
			nonzero++
		}
	}
	if nonzero == 0 {
		t.Fatalf("Embed(%q) produced an all-zero vector", zh)
	}
	// Same words with different spacing tokenize identically.
	vSpaces, _ := h.Embed("用户 登录 失败")
	if got := CosineSimilarity(v, vSpaces); math.Abs(got-1) > 1e-6 {
		t.Errorf("zh similarity with spaces = %v, want 1", got)
	}
	// A subset query shares unigrams and bigrams, so it must rank above an
	// unrelated English query.
	subset, _ := h.Embed("用户登录")
	en, _ := h.Embed("password reset")
	if CosineSimilarity(v, subset) <= CosineSimilarity(v, en) {
		t.Errorf("zh-zh subset similarity (%v) should beat zh-en (%v)",
			CosineSimilarity(v, subset), CosineSimilarity(v, en))
	}
	if CosineSimilarity(v, subset) <= 0 {
		t.Errorf("zh-zh subset similarity = %v, want > 0", CosineSimilarity(v, subset))
	}
	// Japanese and Korean must embed to non-zero vectors as well.
	for _, s := range []string{"テストをしました", "안녕하세요"} {
		vec, err := h.Embed(s)
		if err != nil {
			t.Fatal(err)
		}
		nz := 0
		for _, x := range vec {
			if x != 0 {
				nz++
			}
		}
		if nz == 0 {
			t.Errorf("Embed(%q) produced an all-zero vector", s)
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
