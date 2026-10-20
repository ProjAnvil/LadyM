package cli

import (
	"cmp"
	"fmt"
	"io"

	"github.com/ProjAnvil/LadyM/hook"
	"github.com/spf13/cobra"
)

// hookCmd is `ladym hook`: the harness lifecycle-hook carrier. It reads the
// event JSON from stdin, maps it, writes it through the engine, and exits.
// The hook is a pure observer: stdout stays empty and the exit code is never
// 2, so it never blocks or feeds decisions back into the harness.
func hookCmd() *cobra.Command {
	var db, workspace, harness string
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Record a harness hook event from stdin (observer, never blocks).",
		RunE: func(c *cobra.Command, args []string) error {
			data, err := io.ReadAll(c.InOrStdin())
			if err != nil {
				fmt.Fprintf(c.ErrOrStderr(), "ladym hook: read stdin: %v\n", err)
				return &exitError{1}
			}
			p, err := hook.Parse(data)
			if err != nil {
				fmt.Fprintf(c.ErrOrStderr(), "ladym hook: %v\n", err)
				return &exitError{1}
			}
			ev := p.ToOperation(harness)
			if ev == nil {
				// Unknown event type: tolerate schema drift, exit 0.
				fmt.Fprintf(c.ErrOrStderr(), "ladym hook: ignored event %q\n", p.HookEventName)
				return nil
			}
			ws := cmp.Or(workspace, hook.WorkspaceFromCwd(p.Cwd))
			eng, err := newEngine(db, "")
			if err != nil {
				fmt.Fprintf(c.ErrOrStderr(), "ladym hook: %v\n", err)
				return &exitError{1}
			}
			defer eng.Close()
			if _, err := eng.Scope(ws).RecordOperation(*ev); err != nil {
				fmt.Fprintf(c.ErrOrStderr(), "ladym hook: %v\n", err)
				return &exitError{1}
			}
			return nil
		},
	}
	addDBWS(cmd, &db, &workspace)
	cmd.Flags().StringVar(&harness, "harness", "", "Event source (claude-code | codex); lands in the Agent field.")
	return cmd
}
