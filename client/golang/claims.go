package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/ProjAnvil/LadyM/schema"
)

type EvidenceRef = schema.EvidenceRef
type ClaimScope = schema.ClaimScope
type Verification = schema.Verification
type Claim = schema.Claim
type RecordEvidenceRequest = schema.RecordEvidenceRequest
type CreateClaimRequest = schema.CreateClaimRequest
type ClaimFeedback = schema.ClaimFeedback
type ClaimFeedbackRequest = schema.ClaimFeedbackRequest

func (c *Client) RecordEvidence(ctx context.Context, req RecordEvidenceRequest) (*schema.Memory, error) {
	req.Actor = c.actor
	var out schema.Memory
	err := c.post(ctx, "/api/evidence", req, &out)
	return &out, err
}

func (c *Client) ArchiveEvidence(ctx context.Context, req RecordEvidenceRequest) (*schema.Memory, error) {
	req.Actor = c.actor
	var out schema.Memory
	err := c.post(ctx, "/api/evidence/archive", req, &out)
	return &out, err
}

func (c *Client) CreateClaim(ctx context.Context, req CreateClaimRequest) (*Claim, error) {
	var out Claim
	err := c.post(ctx, "/api/claims", req, &out)
	return &out, err
}

func (c *Client) GetClaim(ctx context.Context, id, workspace string) (*Claim, error) {
	var out Claim
	err := c.do(ctx, http.MethodGet, "/api/claims/"+url.PathEscape(id), url.Values{"workspace": {workspace}}, nil, &out)
	return &out, err
}

func (c *Client) ApplyClaimFeedback(ctx context.Context, id string, req ClaimFeedbackRequest) (*Claim, error) {
	req.Actor = c.actor
	var out Claim
	err := c.post(ctx, "/api/claims/"+url.PathEscape(id)+"/feedback", req, &out)
	return &out, err
}
