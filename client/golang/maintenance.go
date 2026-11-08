package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/ProjAnvil/LadyM/schema"
)

type ScheduleMaintenanceRequest = schema.ScheduleMaintenanceRequest

func (c *Client) ScheduleMaintenance(ctx context.Context, req ScheduleMaintenanceRequest) (*schema.Memory, error) {
	var out schema.Memory
	err := c.post(ctx, "/api/maintenance/jobs", req, &out)
	return &out, err
}
func (c *Client) ListMaintenanceJobs(ctx context.Context, workspace string, limit int) ([]*schema.MaintenanceJob, error) {
	var out struct {
		Jobs []*schema.MaintenanceJob `json:"jobs"`
	}
	err := c.do(ctx, http.MethodGet, "/api/maintenance/jobs", url.Values{"workspace": {workspace}, "limit": {strconv.Itoa(limit)}}, nil, &out)
	return out.Jobs, err
}
func (c *Client) MaintenanceStatus(ctx context.Context, workspace string) (*schema.MaintenanceReport, error) {
	var out schema.MaintenanceReport
	err := c.do(ctx, http.MethodGet, "/api/maintenance/status", url.Values{"workspace": {workspace}}, nil, &out)
	return &out, err
}
