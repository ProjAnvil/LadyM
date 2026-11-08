package schema

// IsRetired preserves the supersedes metadata semantics used by existing data.
func IsRetired(m *Memory) bool {
	return m != nil && (m.MetaString("superseded_by") != "" || m.MetaBool("superseded"))
}

// IsUsable includes legacy knowledge without an explicit status.
func IsUsable(m *Memory) bool {
	return m != nil && !IsRetired(m) && m.MetaString("knowledge_status") != "stale" &&
		m.MetaString("knowledge_status") != "rejected" && m.Meta("evidence_missing") != true
}
