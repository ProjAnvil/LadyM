package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

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
	cmd.AddCommand(hookInstallCmd(), hookUninstallCmd())
	return cmd
}

// hookInstallCmd is `ladym hook install`: writes LadyM observer entries into
// the harness user configs. Flags pick the target; --all covers both.
func hookInstallCmd() *cobra.Command {
	var claude, codex, all bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Write LadyM hook entries into harness user configs.",
		RunE: func(c *cobra.Command, args []string) error {
			var anyTarget bool
			if claude || all {
				p := filepath.Join(homeDir(), ".claude", "settings.json")
				if err := installHookFile(p, "claude-code", claudeHookEvents, false); err != nil {
					return err
				}
				fmt.Fprintf(c.OutOrStdout(), "installed LadyM hooks into %s (restart your session to activate)\n", p)
				anyTarget = true
			}
			if codex || all {
				p := filepath.Join(homeDir(), ".codex", "hooks.json")
				if err := installHookFile(p, "codex", codexHookEvents, true); err != nil {
					return err
				}
				fmt.Fprintf(c.OutOrStdout(), "installed LadyM hooks into %s (run /hooks in Codex to trust them)\n", p)
				anyTarget = true
			}
			if !anyTarget {
				return errors.New("pick one: --claude, --codex, or --all")
			}
			if _, err := exec.LookPath("ladym"); err != nil {
				fmt.Fprintln(os.Stderr, "warning: ladym is not on PATH; hooks will fail to start")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&claude, "claude", false, "Install into ~/.claude/settings.json")
	cmd.Flags().BoolVar(&codex, "codex", false, "Install into ~/.codex/hooks.json")
	cmd.Flags().BoolVar(&all, "all", false, "Install into both")
	return cmd
}

// hookUninstallCmd is `ladym hook uninstall`: removes our entries and only
// ours; a missing config file is a no-op success.
func hookUninstallCmd() *cobra.Command {
	var claude, codex, all bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove LadyM hook entries from harness user configs.",
		RunE: func(c *cobra.Command, args []string) error {
			var anyTarget bool
			if claude || all {
				p := filepath.Join(homeDir(), ".claude", "settings.json")
				if err := uninstallHookFile(p); err != nil {
					return err
				}
				fmt.Fprintf(c.OutOrStdout(), "removed LadyM hooks from %s\n", p)
				anyTarget = true
			}
			if codex || all {
				p := filepath.Join(homeDir(), ".codex", "hooks.json")
				if err := uninstallHookFile(p); err != nil {
					return err
				}
				fmt.Fprintf(c.OutOrStdout(), "removed LadyM hooks from %s\n", p)
				anyTarget = true
			}
			if !anyTarget {
				return errors.New("pick one: --claude, --codex, or --all")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&claude, "claude", false, "Uninstall from ~/.claude/settings.json")
	cmd.Flags().BoolVar(&codex, "codex", false, "Uninstall from ~/.codex/hooks.json")
	cmd.Flags().BoolVar(&all, "all", false, "Uninstall from both")
	return cmd
}

// homeDir resolves the user home (os.UserHomeDir with fallback).
func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}
