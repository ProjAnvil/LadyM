package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type BuildTopicRequest struct {
	Topic            string   `json:"topic"`
	InputIDs         []string `json:"input_ids"`
	ExpectedRevision int64    `json:"expected_revision"`
	Workspace        string   `json:"workspace"`
}

type HybridRecallRequest struct {
	Query     string `json:"query"`
	Limit     int    `json:"limit"`
	Workspace string `json:"workspace"`
}

type BootstrapRequest struct {
	TopicIDs    []string `json:"topic_ids"`
	Environment string   `json:"environment"`
	CodeVersion string   `json:"code_version"`
	Conditions  []string `json:"conditions"`
	TaskID      string   `json:"task_id"`
	Unit        string   `json:"unit"`
	Limit       int      `json:"limit"`
	Workspace   string   `json:"workspace"`
}

type BootstrapResponse struct {
	Content   string   `json:"content"`
	Unit      string   `json:"unit"`
	Model     string   `json:"model"`
	Used      int      `json:"used"`
	SourceIDs []string `json:"source_ids"`
}

type SetTaskStateRequest struct {
	ID        string  `json:"id"`
	Goal      string  `json:"goal"`
	Next      string  `json:"next_step"`
	TTLS      float64 `json:"ttl_s"`
	Workspace string  `json:"workspace"`
}

// SourceFingerprint captures trust, scope and TTL as well as literal content.
// Access statistics are deliberately excluded so reads cannot stale a topic.
func SourceFingerprint(m *Memory) (string, error) {
	if m == nil {
		return "", ErrMemoryNotFound
	}
	raw, err := json.Marshal(struct {
		Content   string
		Workspace string
		Type      MemoryType
		Metadata  map[string]any
	}{m.Content, m.Workspace, m.Type, m.Metadata})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func IsSystemType(t MemoryType) bool {
	switch t {
	case TypeMaintenanceJob, TypeTaskState, TypeEvidence, TypeForwardIntent:
		return true
	}
	return false
}
