package engine

import (
	"strings"
	"testing"
)

func TestArchiveRedactionQuotedCredentials(t *testing.T) {
	for _, input := range []string{
		`{"token":"part1\"part2"}`,
		`api_key='part1\'part2'`,
		`Authorization: Bearer part1`,
		`password="part1 with spaces"`,
	} {
		got := RedactEvidence(input)
		if strings.Contains(got, "part") || !strings.Contains(got, "[REDACTED]") {
			t.Fatalf("credential value partially retained: %q", got)
		}
	}
}
