package cli

import (
	"context"
	"encoding/json"
	"fmt"

	client "github.com/ProjAnvil/LadyM/client/golang"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/spf13/cobra"
)

type claimCommandOptions struct {
	db, workspace, server, user, password string
}

func (o *claimCommandOptions) flags(cmd *cobra.Command) {
	addDBWS(cmd, &o.db, &o.workspace)
	addRemoteFlags(cmd, &o.server, &o.user, &o.password)
}

func (o *claimCommandOptions) run(cmd *cobra.Command, local func(*engine.Scope) (any, error), remote func(context.Context, *client.Client) (any, error)) error {
	if err := remoteGuard(o.db, o.server); err != nil {
		return err
	}
	var out any
	var err error
	if o.server != "" {
		rc := newRemoteClient(o.server, resolveRemoteAuth(o.user, o.password))
		out, err = callRemote(rc, remoteDefaultTimeout, func(ctx context.Context) (any, error) { return remote(ctx, rc.sdk) })
	} else {
		eng, openErr := newEngine(o.db, o.workspace)
		if openErr != nil {
			return openErr
		}
		defer eng.Close()
		out, err = local(eng.Scope(o.workspace))
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
}

func evidenceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "evidence", Short: "Record caller-attested evidence or opt-in redacted archives."}
	for _, archive := range []bool{false, true} {
		name := "record"
		if archive {
			name = "archive"
		}
		var o claimCommandOptions
		var kind, outcome string
		child := &cobra.Command{
			Use: name + " <content>", Short: "Store a provided string; never execute commands or import files.", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				req := schema.RecordEvidenceRequest{Kind: kind, Content: args[0], Outcome: outcome, Workspace: o.workspace}
				return o.run(cmd, func(s *engine.Scope) (any, error) {
					if archive {
						return s.ArchiveEvidence(req.Kind, req.Content, req.Outcome, "cli")
					}
					return s.RecordEvidence(req.Kind, req.Content, req.Outcome, "cli")
				}, func(ctx context.Context, c *client.Client) (any, error) {
					if archive {
						return c.ArchiveEvidence(ctx, req)
					}
					return c.RecordEvidence(ctx, req)
				})
			},
		}
		o.flags(child)
		child.Flags().StringVar(&kind, "kind", "", "observation, test_result, tool_result or user_confirmation")
		child.Flags().StringVar(&outcome, "outcome", "", "Caller-reported outcome (success for test/tool verification)")
		_ = child.MarkFlagRequired("kind")
		cmd.AddCommand(child)
	}
	return cmd
}

func parseClaimRefs(text string) ([]schema.EvidenceRef, error) {
	var refs []schema.EvidenceRef
	if err := json.Unmarshal([]byte(text), &refs); err != nil || refs == nil {
		return nil, fmt.Errorf("--evidence must be a JSON array of evidence refs")
	}
	return refs, nil
}

func claimCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "claim", Short: "Create, read and apply revision-checked claim feedback."}
	cmd.AddCommand(claimCreateCmd(), claimGetCmd(), claimFeedbackCmd())
	return cmd
}

func claimCreateCmd() *cobra.Command {
	var o claimCommandOptions
	var refsJSON, scopeJSON, status string
	var until float64
	cmd := &cobra.Command{
		Use: "create <content>", Short: "Create an inferred or user_asserted claim.", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			refs, err := parseClaimRefs(refsJSON)
			if err != nil {
				return err
			}
			var scope schema.ClaimScope
			var object map[string]json.RawMessage
			if err := json.Unmarshal([]byte(scopeJSON), &object); err != nil || object == nil {
				return fmt.Errorf("--scope must be a JSON object")
			}
			if err := json.Unmarshal([]byte(scopeJSON), &scope); err != nil {
				return fmt.Errorf("--scope: %w", err)
			}
			req := schema.CreateClaimRequest{Content: args[0], Evidence: refs, Scope: scope, Status: status, ValidUntil: until, Workspace: o.workspace}
			return o.run(cmd, func(s *engine.Scope) (any, error) {
				return s.CreateClaim(req.Content, req.Evidence, req.Scope, req.Status, req.ValidUntil)
			}, func(ctx context.Context, c *client.Client) (any, error) { return c.CreateClaim(ctx, req) })
		},
	}
	o.flags(cmd)
	cmd.Flags().StringVar(&refsJSON, "evidence", "[]", "JSON array of scoped evidence refs")
	cmd.Flags().StringVar(&scopeJSON, "scope", "{}", "JSON claim scope: environment, code_version, conditions")
	cmd.Flags().StringVar(&status, "status", "inferred", "inferred or user_asserted")
	cmd.Flags().Float64Var(&until, "valid-until", 0, "Expiry in Unix seconds, 0 means no expiry")
	return cmd
}

func claimGetCmd() *cobra.Command {
	var o claimCommandOptions
	cmd := &cobra.Command{
		Use: "get <id>", Short: "Read a claim, suppressing stale body content.", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.run(cmd, func(s *engine.Scope) (any, error) { return s.GetClaim(args[0]) },
				func(ctx context.Context, c *client.Client) (any, error) { return c.GetClaim(ctx, args[0], o.workspace) })
		},
	}
	o.flags(cmd)
	return cmd
}

func claimFeedbackCmd() *cobra.Command {
	var o claimCommandOptions
	var req schema.ClaimFeedbackRequest
	var refsJSON string
	cmd := &cobra.Command{
		Use: "feedback <id>", Short: "CAS verify, correct or reject a claim.", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			refs, err := parseClaimRefs(refsJSON)
			if err != nil {
				return err
			}
			if req.ExpectedRevision < 1 {
				return fmt.Errorf("--expected-revision must be positive")
			}
			req.Evidence, req.Workspace = refs, o.workspace
			return o.run(cmd, func(s *engine.Scope) (any, error) {
				req.Actor = "cli"
				return s.ApplyClaimFeedback(args[0], req.ClaimFeedback)
			}, func(ctx context.Context, c *client.Client) (any, error) {
				return c.ApplyClaimFeedback(ctx, args[0], req)
			})
		},
	}
	o.flags(cmd)
	cmd.Flags().Int64Var(&req.ExpectedRevision, "expected-revision", 0, "Required positive claim revision")
	cmd.Flags().StringVar(&req.Action, "action", "", "verify, correct or reject")
	cmd.Flags().StringVar(&req.Replacement, "replacement", "", "Corrected claim content")
	cmd.Flags().StringVar(&req.Method, "method", "", "Caller-attested verification method")
	cmd.Flags().StringVar(&req.Reason, "reason", "", "Rejection reason")
	cmd.Flags().StringVar(&refsJSON, "evidence", "[]", "JSON array of scoped evidence refs")
	_ = cmd.MarkFlagRequired("expected-revision")
	_ = cmd.MarkFlagRequired("action")
	return cmd
}
