package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/spf13/cobra"
)

// recallOpsCmd is `ladym recall-ops`: the recall_ops pivot query over
// operation history (resource x action x time window x outcome).
func recallOpsCmd() *cobra.Command {
	var db, workspace, resource, action, outcome string
	var since, until float64
	var limit int
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "recall-ops",
		Short: "Query operation history by resource (pivot: resource x action x time).",
		RunE: func(c *cobra.Command, args []string) error {
			eng, err := newEngine(db, workspace)
			if err != nil {
				return err
			}
			defer eng.Close()
			resp, err := eng.RecallOperations(engine.OperationsQuery{
				Resource: resource,
				Action:   action,
				Since:    since,
				Until:    until,
				Outcome:  outcome,
				Limit:    limit,
			})
			if err != nil {
				return err
			}
			return printRecallOps(resp, jsonOut)
		},
	}
	addDBWS(cmd, &db, &workspace)
	cmd.Flags().StringVar(&resource, "resource", "", "Resource name or alias")
	cmd.Flags().StringVar(&action, "action", "", "Action word (normalized, exact match)")
	cmd.Flags().Float64Var(&since, "since", 0, "Only operations at/after this unix timestamp")
	cmd.Flags().Float64Var(&until, "until", 0, "Only operations at/before this unix timestamp")
	cmd.Flags().StringVar(&outcome, "outcome", "", "Filter by outcome (success|fail)")
	cmd.Flags().IntVar(&limit, "limit", 10, "Max results")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit JSON.")
	_ = cmd.MarkFlagRequired("resource")
	return cmd
}

// printRecallOps emits the recall-ops output: a table by default, a JSON
// payload with --json (mirrors printRecall's two modes).
func printRecallOps(resp *engine.OperationsResponse, jsonOut bool) error {
	if jsonOut {
		var resource any // nil → JSON null on fallback
		if resp.Resource != nil {
			resource = resp.Resource.ID
		}
		payload := map[string]any{
			"resource": resource,
			"fallback": resp.Fallback,
			"items":    recallResultsJSON(resp.Items),
		}
		b, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(resp.Items) == 0 {
		fmt.Println("no memories matched")
		return nil
	}
	if resp.Fallback {
		fmt.Println("recall-ops: resource unresolved, fell back to plain recall")
	} else {
		fmt.Printf("recall-ops: %s\n", resp.Resource.Content)
	}
	writeRecallTable(os.Stdout, &schema.RecallResponse{Results: resp.Items})
	return nil
}
