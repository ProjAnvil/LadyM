package cli

import (
	"context"
	"fmt"

	client "github.com/ProjAnvil/LadyM/client/golang"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/spf13/cobra"
)

func topicCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "topic", Short: "Build or get deterministic source-grounded topic navigation."}
	var buildOptions, getOptions claimCommandOptions
	var req schema.BuildTopicRequest
	build := &cobra.Command{Use: "build <topic>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		req.Topic, req.Workspace = args[0], buildOptions.workspace
		return buildOptions.run(cmd, func(s *engine.Scope) (any, error) { return s.BuildTopic(req.Topic, req.InputIDs, req.ExpectedRevision) }, func(ctx context.Context, c *client.Client) (any, error) { return c.BuildTopic(ctx, req) })
	}}
	buildOptions.flags(build)
	build.Flags().StringSliceVar(&req.InputIDs, "input-ids", nil, "Source IDs (1..64), comma separated")
	build.Flags().Int64Var(&req.ExpectedRevision, "expected-revision", 0, "CAS revision, 0 for absent creation")
	_ = build.MarkFlagRequired("input-ids")
	_ = build.MarkFlagRequired("expected-revision")
	get := &cobra.Command{Use: "get <topic>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return getOptions.run(cmd, func(s *engine.Scope) (any, error) { return s.GetTopic(args[0]) }, func(ctx context.Context, c *client.Client) (any, error) {
			return c.GetTopic(ctx, args[0], getOptions.workspace)
		})
	}}
	getOptions.flags(get)
	cmd.AddCommand(build, get)
	return cmd
}

func hybridRecallCmd() *cobra.Command {
	var o claimCommandOptions
	var req schema.HybridRecallRequest
	cmd := &cobra.Command{Use: "hybrid-recall <query>", Short: "Exact sources, scoped vectors and bounded graph (RRF60).", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		req.Query, req.Workspace = args[0], o.workspace
		return o.run(cmd, func(s *engine.Scope) (any, error) { return s.HybridRecall(req.Query, req.Limit) }, func(ctx context.Context, c *client.Client) (any, error) { return c.HybridRecall(ctx, req) })
	}}
	o.flags(cmd)
	cmd.Flags().IntVar(&req.Limit, "limit", 8, "Maximum results (1..200)")
	return cmd
}

func bootstrapCmd() *cobra.Command {
	var o claimCommandOptions
	var req schema.BootstrapRequest
	cmd := &cobra.Command{Use: "bootstrap", Short: "Bounded verified navigation reference, bytes only.", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if req.Unit != "bytes" {
			return fmt.Errorf("CLI bootstrap supports bytes only; tokens require a host TokenCounter")
		}
		req.Workspace = o.workspace
		return o.run(cmd, func(s *engine.Scope) (any, error) { return s.Bootstrap(req, nil) }, func(ctx context.Context, c *client.Client) (any, error) { return c.Bootstrap(ctx, req) })
	}}
	o.flags(cmd)
	cmd.Flags().StringSliceVar(&req.TopicIDs, "topic-ids", nil, "Explicit topic IDs")
	cmd.Flags().StringVar(&req.Environment, "environment", "", "Caller environment")
	cmd.Flags().StringVar(&req.CodeVersion, "code-version", "", "Caller code version")
	cmd.Flags().StringSliceVar(&req.Conditions, "conditions", nil, "Caller-attested satisfied conditions, exact case")
	cmd.Flags().StringVar(&req.TaskID, "task-id", "", "Explicit saved task ID")
	cmd.Flags().StringVar(&req.Unit, "unit", "bytes", "bytes only")
	cmd.Flags().IntVar(&req.Limit, "limit", 4096, "Full message byte budget (1..4096)")
	return cmd
}

func taskStateCmd() *cobra.Command {
	var o claimCommandOptions
	var req schema.SetTaskStateRequest
	cmd := &cobra.Command{Use: "task-state", Short: "Explicit user goal/next step, never predictions."}
	set := &cobra.Command{Use: "set <id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		req.ID, req.Workspace = args[0], o.workspace
		return o.run(cmd, func(s *engine.Scope) (any, error) { return s.SetTaskState(req.ID, req.Goal, req.Next, req.TTLS) }, func(ctx context.Context, c *client.Client) (any, error) { return c.SetTaskState(ctx, req) })
	}}
	o.flags(set)
	set.Flags().StringVar(&req.Goal, "goal", "", "User-provided goal")
	set.Flags().StringVar(&req.Next, "next-step", "", "User-provided next step")
	set.Flags().Float64Var(&req.TTLS, "ttl-s", 86400, "Expiry seconds, 0 defaults to24h")
	_ = set.MarkFlagRequired("goal")
	cmd.AddCommand(set)
	return cmd
}
