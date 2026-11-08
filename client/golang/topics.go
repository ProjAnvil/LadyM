package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
)

type BuildTopicRequest = schema.BuildTopicRequest
type HybridRecallRequest = schema.HybridRecallRequest
type BootstrapRequest = schema.BootstrapRequest
type BootstrapResponse = schema.BootstrapResponse
type SetTaskStateRequest = schema.SetTaskStateRequest
type TokenCounter = engine.TokenCounter

func (c *Client) BuildTopic(ctx context.Context, req BuildTopicRequest) (*schema.Memory, error) {
	var out schema.Memory
	err := c.post(ctx, "/api/topics", req, &out)
	return &out, err
}
func (c *Client) GetTopic(ctx context.Context, topic, workspace string) (*schema.Memory, error) {
	var out schema.Memory
	err := c.do(ctx, http.MethodGet, "/api/topics/"+url.PathEscape(topic), url.Values{"workspace": {workspace}}, nil, &out)
	return &out, err
}
func (c *Client) HybridRecall(ctx context.Context, req HybridRecallRequest) (*schema.RecallResponse, error) {
	var out schema.RecallResponse
	err := c.post(ctx, "/api/hybrid_recall", req, &out)
	return &out, err
}
func (c *Client) Bootstrap(ctx context.Context, req BootstrapRequest) (*BootstrapResponse, error) {
	if req.Unit != "" && req.Unit != "bytes" {
		return nil, fmt.Errorf("remote bootstrap supports bytes only; use BootstrapLocal with a host TokenCounter")
	}
	var out BootstrapResponse
	err := c.post(ctx, "/api/bootstrap", req, &out)
	return &out, err
}
func (c *Client) SetTaskState(ctx context.Context, req SetTaskStateRequest) (*schema.Memory, error) {
	var out schema.Memory
	err := c.post(ctx, "/api/task_state", req, &out)
	return &out, err
}

// BootstrapLocal uses the host's exact tokenizer, without remote model guesses.
func BootstrapLocal(scope *engine.Scope, req BootstrapRequest, counter TokenCounter) (*BootstrapResponse, error) {
	return scope.Bootstrap(req, counter)
}
