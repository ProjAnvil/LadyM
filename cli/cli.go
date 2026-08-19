// Package cli holds the LadyM command-line interface (cobra-based).
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/ProjAnvil/LadyM/code"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/mcp"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/secrets"
	"github.com/ProjAnvil/LadyM/storage"
	"github.com/ProjAnvil/LadyM/web"
	"github.com/spf13/cobra"
)

var (
	globalConfigPath string
	globalDebug      bool
)

// exitError carries a process exit code with no message — used when the
// command already printed its own diagnostic (mirrors Python's typer.Exit).
// Execute exits with the code WITHOUT printing a second error line.
type exitError struct{ code int }

func (e *exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

// Execute runs the CLI and exits non-zero on error.
func Execute() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		var exitErr *exitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.code)
		}
		var cfgErr *config.ConfigError
		if errors.As(err, &cfgErr) && !globalDebug {
			fmt.Fprintf(os.Stderr, "ladym: %s\n", cfgErr.Msg)
			os.Exit(1)
		}
		if !globalDebug {
			fmt.Fprintf(os.Stderr, "ladym: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "%+v\n", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "ladym",
		Short:         "LadyM — brain-inspired memory for LLM agents & codebase RAG.",
		Version:       storage.Edition,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&globalConfigPath, "config", "", "Path to a ladym.toml to load on top of defaults/env.")
	root.PersistentFlags().BoolVar(&globalDebug, "debug", false, "Show full error details on error.")

	root.AddCommand(
		rememberCmd(), recordCmd(), recallCmd(), indexCmd(), consolidateCmd(),
		statsCmd(), forgetCmd(), linkCmd(), serveCmd(), workerCmd(), configCmd(),
	)
	return root
}

func loadConfig(db, workspace string) (*config.Config, error) {
	overrides := map[string]any{}
	if db != "" {
		overrides["db_path"] = db
	}
	if workspace != "" {
		overrides["workspace"] = workspace
	}
	return config.Load(globalConfigPath, overrides)
}

func newEngine(db, workspace string) (*engine.Engine, error) {
	cfg, err := loadConfig(db, workspace)
	if err != nil {
		return nil, err
	}
	return engine.New(cfg)
}

func addDBWS(cmd *cobra.Command, db, workspace *string) {
	cmd.Flags().StringVar(db, "db", "", "Path to ladym.db")
	cmd.Flags().StringVarP(workspace, "workspace", "w", "", "Workspace name")
}

func rememberCmd() *cobra.Command {
	var db, workspace, tags, source string
	cmd := &cobra.Command{
		Use:   "remember <content>",
		Short: "Write a semantic memory (fact).",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			eng, err := newEngine(db, workspace)
			if err != nil {
				return err
			}
			defer eng.Close()
			tagList := splitTags(tags)
			m, err := eng.Remember(args[0], schema.LayerSemantic, schema.TypeFact, tagList, nil, source, "")
			if err != nil {
				return err
			}
			if m.MetaString("gated") == "dropped" {
				fmt.Printf("dropped reason=%s (gated; not persisted)\n", m.MetaString("reason"))
			} else {
				fmt.Printf("remembered id=%s hash=%s\n", m.ID, shortHash(m.ContentHash))
			}
			return nil
		},
	}
	addDBWS(cmd, &db, &workspace)
	cmd.Flags().StringVar(&tags, "tags", "", "Comma-separated tags")
	cmd.Flags().StringVar(&source, "source", "cli", "Source label")
	return cmd
}

func recordCmd() *cobra.Command {
	var db, workspace, agent, action, observation, outcome, tags string
	cmd := &cobra.Command{
		Use:   "record",
		Short: "Record an L1 episodic event.",
		RunE: func(c *cobra.Command, args []string) error {
			eng, err := newEngine(db, workspace)
			if err != nil {
				return err
			}
			defer eng.Close()
			m, err := eng.RecordEvent(agent, action, observation, outcome, splitTags(tags), nil)
			if err != nil {
				return err
			}
			fmt.Printf("recorded id=%s layer=%s type=%s\n", m.ID, m.Layer, m.Type)
			return nil
		},
	}
	addDBWS(cmd, &db, &workspace)
	cmd.Flags().StringVar(&agent, "agent", "", "Who/what performed the action.")
	cmd.Flags().StringVar(&action, "action", "", "What was done.")
	cmd.Flags().StringVar(&observation, "observation", "", "What was seen/learned.")
	cmd.Flags().StringVar(&outcome, "outcome", "", "Result of the action.")
	cmd.Flags().StringVar(&tags, "tags", "", "Comma-separated tags")
	_ = cmd.MarkFlagRequired("agent")
	_ = cmd.MarkFlagRequired("action")
	return cmd
}

func recallCmd() *cobra.Command {
	var db, workspace string
	var topK int
	var codeOnly, jsonOut bool
	cmd := &cobra.Command{
		Use:   "recall <query>",
		Short: "Recall memories matching the query.",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			eng, err := newEngine(db, workspace)
			if err != nil {
				return err
			}
			defer eng.Close()
			var resp *schema.RecallResponse
			if codeOnly {
				resp, err = eng.SearchCode(args[0], topK, "")
			} else {
				resp, err = eng.Recall(args[0], "", topK, nil, nil, 0)
			}
			if err != nil {
				return err
			}
			if jsonOut {
				payload := map[string]any{
					"query": resp.Query, "tier_reached": resp.TierReached,
					"reflected_sufficient": resp.ReflectedSufficient, "elapsed_ms": resp.ElapsedMs,
					"results": recallResultsJSON(resp.Results),
				}
				b, _ := json.MarshalIndent(payload, "", "  ")
				fmt.Println(string(b))
				return nil
			}
			if len(resp.Results) == 0 {
				fmt.Println("no memories matched")
				return nil
			}
			fmt.Printf("recall: %s  (tier %d, %.1fms)\n", resp.Query, resp.TierReached, resp.ElapsedMs)
			writeRecallTable(os.Stdout, resp)
			return nil
		},
	}
	addDBWS(cmd, &db, &workspace)
	cmd.Flags().IntVarP(&topK, "top-k", "n", 8, "Number of results")
	cmd.Flags().BoolVar(&codeOnly, "code", false, "Restrict to code items.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit JSON.")
	return cmd
}

// writeRecallTable prints recall results as a table; the memory id is the
// first column so scenario playbooks can assert on ids directly.
func writeRecallTable(w io.Writer, resp *schema.RecallResponse) {
	fmt.Fprintf(w, "%-34s %-10s %-14s %-14s %-60s %s\n", "id", "score", "layer", "type", "summary", "source")
	for _, r := range resp.Results {
		fmt.Fprintf(w, "%-34s %-10.3f %-14s %-14s %-60s %s\n", r.Memory.ID, r.Score, r.Memory.Layer, r.Memory.Type, truncateStr(r.Memory.Summary, 60), truncateStr(r.Memory.Source, 30))
	}
}

func recallResultsJSON(results []*schema.RecallResult) []map[string]any {
	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		out = append(out, map[string]any{
			"id": r.Memory.ID, "layer": r.Memory.Layer, "type": r.Memory.Type,
			"score": r.Score, "tier": r.Tier, "summary": r.Memory.Summary,
			"content": truncateStr(r.Memory.Content, 400), "source": r.Memory.Source,
		})
	}
	return out
}

func indexCmd() *cobra.Command {
	var db, workspace, languages string
	var force bool
	cmd := &cobra.Command{
		Use:   "index <root>",
		Short: "Index a codebase into L2 semantic memory.",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			eng, err := newEngine(db, workspace)
			if err != nil {
				return err
			}
			defer eng.Close()
			var langs []string
			if languages != "" {
				for _, l := range strings.Split(languages, ",") {
					langs = append(langs, strings.TrimSpace(l))
				}
			}
			report, err := eng.IndexCode(args[0], force, "", langs)
			if err != nil {
				return err
			}
			writeIndexReport(os.Stdout, report)
			return nil
		},
	}
	addDBWS(cmd, &db, &workspace)
	cmd.Flags().BoolVar(&force, "force", false, "Re-index even if unchanged.")
	cmd.Flags().StringVar(&languages, "languages", "", "Comma-separated, e.g. python,go")
	return cmd
}

func consolidateCmd() *cobra.Command {
	var db, workspace string
	cmd := &cobra.Command{
		Use:   "consolidate",
		Short: "Promote episodic events into semantic facts.",
		RunE: func(c *cobra.Command, args []string) error {
			eng, err := newEngine(db, workspace)
			if err != nil {
				return err
			}
			defer eng.Close()
			report, err := eng.Consolidate("", 0)
			if err != nil {
				return err
			}
			fmt.Printf("consolidated %d episodes: ADD=%d UPDATE=%d DELETE=%d NOOP=%d\n",
				report.KeptEpisodes, report.Actions["ADD"], report.Actions["UPDATE"],
				report.Actions["DELETE"], report.Actions["NOOP"])
			return nil
		},
	}
	addDBWS(cmd, &db, &workspace)
	return cmd
}

func statsCmd() *cobra.Command {
	var db, workspace string
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show memory statistics.",
		RunE: func(c *cobra.Command, args []string) error {
			eng, err := newEngine(db, workspace)
			if err != nil {
				return err
			}
			defer eng.Close()
			s, err := eng.Stats()
			if err != nil {
				return err
			}
			writeStats(os.Stdout, s, workspace)
			return nil
		},
	}
	addDBWS(cmd, &db, &workspace)
	return cmd
}

func forgetCmd() *cobra.Command {
	var db, workspace string
	cmd := &cobra.Command{
		Use:   "forget <memory_id>",
		Short: "Delete a single memory by id.",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			eng, err := newEngine(db, workspace)
			if err != nil {
				return err
			}
			defer eng.Close()
			if err := eng.Forget(args[0]); err != nil {
				return err
			}
			fmt.Printf("forgot %s\n", args[0])
			return nil
		},
	}
	addDBWS(cmd, &db, &workspace)
	return cmd
}

func linkCmd() *cobra.Command {
	var db, workspace, relation string
	cmd := &cobra.Command{
		Use:   "link <src> <dst>",
		Short: "Create an associative edge between two memories.",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			eng, err := newEngine(db, workspace)
			if err != nil {
				return err
			}
			defer eng.Close()
			edge, err := eng.Link(args[0], args[1], relation)
			if err != nil {
				return err
			}
			fmt.Printf("linked %s -[%s]-> %s (id=%s)\n", args[0], relation, args[1], edge.ID)
			return nil
		},
	}
	addDBWS(cmd, &db, &workspace)
	cmd.Flags().StringVarP(&relation, "relation", "r", "related_to", "Edge relation")
	return cmd
}

func serveCmd() *cobra.Command {
	var db, workspace string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the LadyM MCP server over stdio.",
		RunE: func(c *cobra.Command, args []string) error {
			cfg, err := loadConfig(db, workspace)
			if err != nil {
				return err
			}
			// MCP stdio: stdout must carry ONLY JSON-RPC frames — the startup
			// banner goes to stderr (mirrors the Python `ladym serve`).
			fmt.Fprint(os.Stderr, serveBanner(cfg))
			return mcp.Run(cfg)
		},
	}
	addDBWS(cmd, &db, &workspace)
	return cmd
}

func workerCmd() *cobra.Command {
	var db, workspace string
	var once bool
	var interval int
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Run System2 consolidation cycles in the background.",
		RunE: func(c *cobra.Command, args []string) error {
			cfg, err := loadConfig(db, workspace)
			if err != nil {
				return err
			}
			cfg.EnableWAL = true
			eng, err := engine.New(cfg)
			if err != nil {
				return err
			}
			defer eng.Close()
			return runWorkerLoop(eng, once, interval, workspace)
		},
	}
	addDBWS(cmd, &db, &workspace)
	cmd.Flags().BoolVar(&once, "once", false, "Run one cycle and exit.")
	cmd.Flags().IntVar(&interval, "interval", 300, "Seconds between cycles.")
	return cmd
}

func configCmd() *cobra.Command {
	var port int
	var noBrowser bool
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage ladym.toml (web editor) and the encrypted secret store.",
		// With no subcommand, launch the local web config editor (mirrors the
		// Python `ladym config` behaviour).
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			return web.Run(globalConfigPath, port, noBrowser)
		},
	}
	cmd.Flags().IntVar(&port, "port", 8765, "Listen port")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Do not open a browser")
	cmd.AddCommand(
		configSetCmd(), configSetMasterKeyCmd(), configResetMasterKeyCmd(),
		configListCmd(), configRMCmd(),
	)
	return cmd
}

func configSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <KEY> <VALUE>",
		Short: "Store KEY=VALUE in the encrypted secret store.",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			if err := secrets.NewStore("").Set(args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("stored %s\n", args[0])
			return nil
		},
	}
}

func configSetMasterKeyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-master-key [key]",
		Short: "Initialize the master key.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			key := ""
			if len(args) == 1 {
				key = args[0]
			}
			store := secrets.NewStore("")
			if _, err := store.SetMasterKey(key); err != nil {
				return err
			}
			if key == "" {
				fmt.Printf("generated a random master key at %s — back it up; losing it makes secrets unrecoverable.\n", store.MasterKeyPath())
			} else {
				fmt.Println("master key set")
			}
			return nil
		},
	}
}

func configResetMasterKeyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset-master-key [key]",
		Short: "Re-encrypt every secret under a new master key.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			key := ""
			if len(args) == 1 {
				key = args[0]
			}
			if err := secrets.NewStore("").ResetMasterKey(key); err != nil {
				return err
			}
			fmt.Println("master key reset; all secrets re-encrypted")
			return nil
		},
	}
}

func configListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List stored KEY_NAMEs (values are never printed).",
		RunE: func(c *cobra.Command, args []string) error {
			names, err := secrets.NewStore("").ListNames()
			if err != nil {
				return err
			}
			if len(names) == 0 {
				fmt.Println("no secrets stored")
				return nil
			}
			for _, n := range names {
				fmt.Println(n)
			}
			return nil
		},
	}
}

func configRMCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <KEY>",
		Short: "Remove a stored secret.",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			removed, err := secrets.NewStore("").Remove(args[0])
			if err != nil {
				return err
			}
			if removed {
				fmt.Fprintf(c.OutOrStdout(), "removed %s\n", args[0])
			} else {
				// Python: print "no such key KEY" once and exit(1) — return a
				// silent exitError so Execute does not print a second line.
				fmt.Fprintf(c.OutOrStdout(), "no such key %s\n", args[0])
				return &exitError{code: 1}
			}
			return nil
		},
	}
}

// ---- small helpers ----

// serveBanner is the stderr startup banner printed by `ladym serve` (mirrors
// the Python CLI).
func serveBanner(cfg *config.Config) string {
	return fmt.Sprintf("LadyM MCP server starting (db=%s, ws=%s)\n", cfg.DBPath, cfg.Workspace)
}

// writeIndexReport prints the `index` summary; only the first 5 errors are
// listed (mirrors Python's report.errors[:5]).
func writeIndexReport(w io.Writer, report *code.IndexReport) {
	fmt.Fprintf(w, "indexed %d/%d files (%d symbols, %d refs) in %.0fms\n",
		report.FilesIndexed, report.FilesSeen, report.SymbolsWritten, report.RefsWritten, report.ElapsedMs)
	if report.FilesSkippedUnchanged > 0 {
		fmt.Fprintf(w, "  skipped unchanged: %d\n", report.FilesSkippedUnchanged)
	}
	if len(report.Errors) > 0 {
		fmt.Fprintf(w, "  errors: %d\n", len(report.Errors))
		for i, e := range report.Errors {
			if i >= 5 {
				break
			}
			fmt.Fprintf(w, "    %s\n", e)
		}
	}
}

// writeStats prints the `stats` output, including the by-layer breakdown and
// "(none)" for empty workspaces (mirrors Python cli.py stats()). When
// scopedWS is non-empty (CLI -w), the workspaces line lists only that
// workspace instead of every workspace in the db.
func writeStats(w io.Writer, s *schema.Stats, scopedWS string) {
	fmt.Fprintf(w, "LadyM stats  db=%s\n", s.DBPath)
	fmt.Fprintf(w, "  total memories: %d\n", s.TotalMemories)
	fmt.Fprintf(w, "  edges: %d    code symbols: %d\n", s.Edges, s.CodeSymbols)
	wsList := s.Workspaces
	if scopedWS != "" {
		wsList = []string{scopedWS}
	}
	ws := strings.Join(wsList, ", ")
	if ws == "" {
		ws = "(none)"
	}
	fmt.Fprintf(w, "  workspaces: %s\n", ws)
	if len(s.ByLayer) > 0 {
		fmt.Fprintln(w, "  by layer:")
		layers := make([]string, 0, len(s.ByLayer))
		for k := range s.ByLayer {
			layers = append(layers, k)
		}
		sort.Strings(layers)
		for _, k := range layers {
			fmt.Fprintf(w, "    %-16s %d\n", k, s.ByLayer[k])
		}
	}
}

func splitTags(s string) []string {
	if s == "" {
		return []string{}
	}
	var out []string
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
