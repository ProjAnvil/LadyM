package schema

import "testing"

func TestClaimFingerprintIgnoresAccess(t *testing.T) {
	m := NewMemory(LayerSemantic, TypeFact)
	m.Content = "hello"
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if KnowledgeFingerprint(m) != want {
		t.Fatal("fingerprint is not SHA256 of content")
	}
	m.Touch()
	m.Summary, m.Metadata["scope"] = "changed summary", "changed scope"
	if KnowledgeFingerprint(m) != want {
		t.Fatal("fingerprint included noncontent fields")
	}
	m.Content = "changed"
	if KnowledgeFingerprint(m) == want {
		t.Fatal("content mutation retained fingerprint")
	}
}
