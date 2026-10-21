//go:build eval

package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// writeJSON persists the machine-readable metrics.
func writeJSON(path string, rep Report) error {
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// writeMarkdown renders the human-readable report: one table per corpus scale,
// then the per-scale stage breakdown, then the caveats that make the numbers
// honest to read.
func writeMarkdown(path string, rep Report) error {
	var b strings.Builder
	b.WriteString("# ladyM hook write-path evaluation\n\n")
	fmt.Fprintf(&b, "Generated: %s\n\n", rep.GeneratedAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(&b, "Binary: `%s`\n\n", rep.Bin)
	fmt.Fprintf(&b, "Trials per scenario: %d\n\n", rep.Trials)

	scales := []string{}
	seen := map[string]bool{}
	for _, s := range rep.Scenarios {
		if !seen[s.Scale] {
			seen[s.Scale] = true
			scales = append(scales, s.Scale)
		}
	}
	sort.Strings(scales)

	for _, sc := range scales {
		var rows [][]string
		head := []string{"fixture", "event", "cold ms", "warm p50 ms", "warm p95 ms", "tokens p50", "tokens p95", "amplification p50", "rows"}
		for _, s := range rep.Scenarios {
			if s.Scale != sc {
				continue
			}
			rows = append(rows, []string{
				s.Fixture, s.Event,
				fmt.Sprintf("%.2f", s.SpawnColdMs.P50),
				fmt.Sprintf("%.2f", s.SpawnWarmMs.P50),
				fmt.Sprintf("%.2f", s.SpawnWarmMs.P95),
				fmt.Sprintf("%.0f", s.PersistedToks.P50),
				fmt.Sprintf("%.0f", s.PersistedToks.P95),
				fmt.Sprintf("%.2fx", s.Amplification.P50),
				fmt.Sprintf("%d", s.NewRows),
			})
		}
		fmt.Fprintf(&b, "## Scale: %s\n\n", sc)
		fmt.Fprintf(&b, "`cold` is the first invocation on a freshly copied corpus; `warm p50` is steady state once the file is hot.\n\n")
		b.WriteString(table(head, rows))
		b.WriteString("\n")
	}

	var stageRows [][]string
	stageHead := []string{"scale", "corpus rows", "scope ms", "normalize ms", "resolve ms", "record ms", "edges ms", "total ms", "real ms", "drift"}
	for _, s := range rep.Stages {
		stageRows = append(stageRows, []string{
			s.Scale, fmt.Sprintf("%d", s.Corpus),
			fmt.Sprintf("%.3f", s.Segments.ScopeMs),
			fmt.Sprintf("%.3f", s.Segments.NormalizeMs),
			fmt.Sprintf("%.3f", s.Segments.ResolveMs),
			fmt.Sprintf("%.3f", s.Segments.RecordMs),
			fmt.Sprintf("%.3f", s.Segments.EdgesMs),
			fmt.Sprintf("%.3f", s.Segments.TotalMs),
			fmt.Sprintf("%.3f", s.Segments.RealMs),
			fmt.Sprintf("%.3f", s.Segments.Drift),
		})
	}
	b.WriteString("## Write-path stage breakdown\n\n")
	b.WriteString(table(stageHead, stageRows))

	var driftNote string
	for _, s := range rep.Stages {
		if s.Segments.Drift > DriftTolerance {
			driftNote = fmt.Sprintf(
				"\n> **Warning:** the %s scale shows drift %.3f above the %.2f tolerance, "+
					"so its stage breakdown does not track the real write path and should be read "+
					"as approximate.\n", s.Scale, s.Segments.Drift, DriftTolerance)
			break
		}
	}
	if driftNote != "" {
		b.WriteString(driftNote)
	}

	b.WriteString(`
## How to read these numbers

- **cold ms** is the first invocation on a freshly copied corpus file: cold page
  cache, worst case a session pays on its very first hook call.
- **warm p50 ms** is steady state on an already-hot file, which is what a working
  session pays for every subsequent tool call. This is the number that matters
  for perceived turn latency, because the hook spawns a fresh process per event
  and the database file stays hot for the whole session.
- **In-process stages** (` + "`" + `record ms` + "`" + `, ` + "`" + `edges ms` + "`" + `, and friends) exclude process
  startup entirely. Comparing them against ` + "`" + `warm p50 ms` + "`" + ` shows how much of the wait
  is process tax versus actual write work.
- **tokens** count what persisted per row: content plus summary plus metadata,
  encoded with Cl100kBase. They are comparable across fixtures and scales, not
  billing-accurate for any vendor.
- **amplification** is persisted tokens divided by the tokens the harness sent on
  stdin. Above 1.0 means ladyM stores more than was captured; observation and
  outcome are written to both Content and Metadata, which drives this above 1.

## Caveats

- Corpora are synthetic. Content length follows a log-normal distribution with a
  mixed vocabulary including CJK, but real libraries mix L1/L2/L4 far more richly.
- Absolute durations depend on the machine. Compare against a baseline taken on
  the same box; do not compare CI values to a laptop.
- The write path consumes no LLM tokens. The hook never calls one, and provider
  usage statistics do not exist in this codebase, so no LLM token figure appears
  here.
`)
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// table renders aligned markdown rows.
func table(head []string, rows [][]string) string {
	widths := make([]int, len(head))
	for i, h := range head {
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	var b strings.Builder
	for i, h := range head {
		b.WriteString("| " + h + strings.Repeat(" ", widths[i]-len(h)) + " ")
	}
	b.WriteString("|\n")
	for i := range head {
		b.WriteString("|" + strings.Repeat("-", widths[i]+2) + "|")
	}
	b.WriteString("\n")
	for _, r := range rows {
		for i, c := range r {
			b.WriteString("| " + c + strings.Repeat(" ", widths[i]-len(c)) + " ")
		}
		b.WriteString("|\n")
	}
	return b.String()
}
