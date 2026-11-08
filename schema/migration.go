package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// EmbeddingIdentity names a vector space, including the tokenizer/version.
type EmbeddingIdentity struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Tokenizer string `json:"tokenizer"`
	Dim       int    `json:"dim"`
}

func (i EmbeddingIdentity) Validate() error {
	for _, value := range []string{i.Provider, i.Model, i.Tokenizer} {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "", "unknown", "injected", "unspecified":
			return errors.New("embedding identity requires meaningful provider, model/version and tokenizer/version")
		}
	}
	if i.Dim <= 0 {
		return errors.New("embedding dimension must be positive")
	}
	return nil
}

func (i EmbeddingIdentity) Fingerprint() string {
	i.Provider = strings.TrimSpace(i.Provider)
	i.Model = strings.TrimSpace(i.Model)
	i.Tokenizer = strings.TrimSpace(i.Tokenizer)
	raw, _ := json.Marshal(i)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

type MigrationStatus struct {
	State             string            `json:"state"`
	ActiveFingerprint string            `json:"active_fingerprint"`
	TargetFingerprint string            `json:"target_fingerprint,omitempty"`
	Generation        string            `json:"generation,omitempty"`
	Cursor            string            `json:"cursor,omitempty"`
	Processed         int               `json:"processed"`
	RollbackAvailable bool              `json:"rollback_available"`
	ActiveIdentity    EmbeddingIdentity `json:"active_identity"`
	TargetIdentity    EmbeddingIdentity `json:"target_identity"`
}
