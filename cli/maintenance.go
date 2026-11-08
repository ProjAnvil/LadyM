package cli

import (
	"context"

	client "github.com/ProjAnvil/LadyM/client/golang"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/spf13/cobra"
)

func maintenanceCmd() *cobra.Command {
	root := &cobra.Command{Use: "maintenance", Short: "Request bounded maintenance jobs or inspect persisted status."}
	var o, listOptions, statusOptions claimCommandOptions
	var req schema.ScheduleMaintenanceRequest
	request := &cobra.Command{Use: "request <operation> <target-id>", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		req.Operation, req.TargetID, req.Workspace = args[0], args[1], o.workspace
		return o.run(cmd, func(s *engine.Scope) (any, error) {
			return s.ScheduleMaintenance(req.Operation, req.TargetID, req.InputIDs, req.Args)
		}, func(ctx context.Context, c *client.Client) (any, error) { return c.ScheduleMaintenance(ctx, req) })
	}}
	o.flags(request)
	request.Flags().StringSliceVar(&req.InputIDs, "input-ids", nil, "Explicit input IDs, comma separated")
	var limit int
	list := &cobra.Command{Use: "jobs", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return listOptions.run(cmd, func(s *engine.Scope) (any, error) { return s.ListMaintenanceJobs(limit) }, func(ctx context.Context, c *client.Client) (any, error) {
			return c.ListMaintenanceJobs(ctx, listOptions.workspace, limit)
		})
	}}
	listOptions.flags(list)
	list.Flags().IntVar(&limit, "limit", 100, "Maximum jobs (1..1000)")
	status := &cobra.Command{Use: "status", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return statusOptions.run(cmd, func(s *engine.Scope) (any, error) { return s.MaintenanceStatus() }, func(ctx context.Context, c *client.Client) (any, error) {
			return c.MaintenanceStatus(ctx, statusOptions.workspace)
		})
	}}
	statusOptions.flags(status)
	root.AddCommand(request, list, status)
	return root
}
