//go:build eval

package eval

import (
	"fmt"
	"math"
	"math/rand"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/layers"
	"github.com/ProjAnvil/LadyM/operations"
	"github.com/ProjAnvil/LadyM/schema"
)

// Scale names a pre-existing corpus size to measure against.
type Scale struct {
	Name string
	Size int
}

// StandardScales are the corpus sizes this harness measures: roughly a few
// days, a few months, and years of accumulated history in one workspace.
var StandardScales = []Scale{
	{Name: "small", Size: 100},
	{Name: "medium", Size: 1000},
	{Name: "large", Size: 10000},
}

// corpus shaping constants.
const (
	// episodesPerResource bounds how many episodes share one file entity. A
	// bounded pool is realistic and makes resource resolution scale visibly.
	episodesPerResource = 20
	minResourcePool     = 8

	// contentLengthMedian and contentLengthSigma shape a log-normal content
	// length distribution, in characters. Real captures are wildly uneven:
	// one-word tool results next to 2k-rune prompts.
	contentLengthMedian = 160.0
	contentLengthSigma  = 0.9
	minContentLength    = 8
	maxContentLength    = 4000
)

// Seed builds size synthetic memories in their own database file and returns
// that path. It writes through the episodic layer directly rather than through
// Remember, so the attention gate cannot drop rows and skew counts.
//
// Seeding is deterministic for a given seed: content comes from a seeded PRNG
// and embeddings use the offline hashing provider, so two calls with the same
// arguments produce byte-identical rows.
func Seed(size int, dir string, seed int64, workspace string) (string, error) {
	dbPath := filepath.Join(dir, "corpus.ladym.db")
	cfg := config.ForTesting(dir)
	cfg.DBPath = dbPath
	cfg.Workspace = workspace

	eng, err := engine.New(cfg)
	if err != nil {
		return "", fmt.Errorf("engine for corpus: %w", err)
	}
	defer eng.Close()

	ep := layers.NewEpisodicMemory(eng.Store, eng.Provider, workspace)
	rng := rand.New(rand.NewSource(seed))
	pool := resourcePool(rng, size)
	agents := []string{"claude-code", "codex"}
	actions := []string{"tool:Read", "tool:Edit", "tool:Bash", "tool:Grep", "prompt_submit"}

	for i := 0; i < size; i++ {
		name := pool[rng.Intn(len(pool))]
		res, _, err := operations.ResolveResource(eng.Store, eng.Provider, cfg, workspace, name)
		if err != nil {
			return "", fmt.Errorf("resolve resource %q: %w", name, err)
		}
		m, err := ep.Record(
			agents[i%len(agents)],
			actions[i%len(actions)],
			sampleObservation(rng, i),
			"success",
			[]string{"synthetic"},
			map[string]any{"synthetic": true, "seq": i},
		)
		if err != nil {
			return "", fmt.Errorf("record synthetic episode %d: %w", i, err)
		}
		if err := linkResource(eng, m.ID, res.ID); err != nil {
			return "", fmt.Errorf("link episode %d to %q: %w", i, name, err)
		}
	}
	return dbPath, nil
}

// linkResource writes the same deterministic projected edge RecordOperation
// would, so a seeded corpus has the same shape the hook produces.
func linkResource(eng *engine.Engine, episodeID, resourceID string) error {
	e := schema.NewEdge(episodeID, "operates_on", resourceID)
	e.ID = schema.ProjectedEdgeID(episodeID, "operates_on", resourceID)
	return eng.Store.PutEdge(e)
}

// resourcePool returns the distinct file entities this corpus references. The
// result is sorted so selection stays deterministic even though the names were
// drawn from the same PRNG stream as the content.
func resourcePool(rng *rand.Rand, size int) []string {
	n := size / episodesPerResource
	if n < minResourcePool {
		n = minResourcePool
	}
	pkgs := []string{"engine", "storage", "operations", "cli", "mcp", "layers", "providers", "config"}
	exts := []string{".go", ".md", ".toml", ".json"}
	seen := map[string]bool{}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("%s_%d%s", pkgs[rng.Intn(len(pkgs))], rng.Intn(1000), exts[rng.Intn(len(exts))])
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	if len(out) == 0 {
		out = append(out, "engine_0.go")
	}
	sort.Strings(out)
	return out
}

// sampleObservation draws content of log-normal length so token statistics span
// the spread real captures have, rather than one uniform blob size.
func sampleObservation(rng *rand.Rand, seq int) string {
	target := int(math.Exp(math.Log(contentLengthMedian) + contentLengthSigma*rng.NormFloat64()))
	if target < minContentLength {
		target = minContentLength
	}
	if target > maxContentLength {
		target = maxContentLength
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("seq=%d ", seq))
	for utf8.RuneCountInString(b.String()) < target {
		b.WriteString(pickWord(rng))
		b.WriteString(" ")
	}
	return strings.TrimSpace(truncateRunes(b.String(), target))
}

// truncateRunes cuts to n runes, never bytes: slicing bytes would split multi-byte
// characters and corrupt the very input that exercises CJK tokenization.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// pickWord draws from a mixed vocabulary including CJK so token counting meets
// the multi-byte paths real captures exercise.
func pickWord(rng *rand.Rand) string {
	words := []string{
		"refactor", "index", "resolve", "pointer", "cache", "workspace",
		"embedding", "threshold", "migrate", "schema", "panic", "retry",
		"评测", "记忆", "索引", "重构",
	}
	return words[rng.Intn(len(words))]
}
