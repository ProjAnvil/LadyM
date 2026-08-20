package client

// Management-console CRUD (api/crud.go, spec §3.1): memories list/update/
// delete and the admin-only users management API. All endpoints sit behind
// the same Basic-auth middleware; the users endpoints additionally require
// an admin account (403 otherwise).

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/ProjAnvil/LadyM/schema"
)

// MemoryFilter filters/paginates ListMemories. Zero Limit/Offset are omitted
// so the server defaults apply (limit 50, offset 0).
type MemoryFilter struct {
	Workspace string
	Layer     string
	Type      string
	Limit     int
	Offset    int
}

// MemoryList is one page of GET /api/memories. Total is the filtered count
// before pagination.
type MemoryList struct {
	Memories []*schema.Memory `json:"memories"`
	Total    int              `json:"total"`
}

// ListMemories lists memories with workspace/layer/type filters.
func (c *Client) ListMemories(ctx context.Context, f MemoryFilter) (*MemoryList, error) {
	q := url.Values{}
	if f.Workspace != "" {
		q.Set("workspace", f.Workspace)
	}
	if f.Layer != "" {
		q.Set("layer", f.Layer)
	}
	if f.Type != "" {
		q.Set("type", f.Type)
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	if f.Offset > 0 {
		q.Set("offset", strconv.Itoa(f.Offset))
	}
	var out MemoryList
	if err := c.do(ctx, http.MethodGet, "/api/memories", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MemoryPatch is a partial memory update; nil fields are left unchanged.
type MemoryPatch struct {
	Content *string  `json:"content,omitempty"`
	Summary *string  `json:"summary,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

// UpdateMemory patches content/summary/tags of one memory. A content change
// re-embeds server-side.
func (c *Client) UpdateMemory(ctx context.Context, id string, patch MemoryPatch) error {
	return c.do(ctx, http.MethodPut, "/api/memories/"+url.PathEscape(id), nil, patch, nil)
}

// DeleteMemory deletes one memory by id; a missing id is a 404 *Error.
func (c *Client) DeleteMemory(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/memories/"+url.PathEscape(id), nil, nil, nil)
}

// UserPatch is a partial account update; nil fields are left unchanged. An
// explicitly empty Password makes the account passwordless.
type UserPatch struct {
	Password  *string `json:"password,omitempty"`
	Workspace *string `json:"workspace,omitempty"`
	Admin     *bool   `json:"admin,omitempty"`
}

// ListUsers lists all accounts (admin only). The returned Users never carry
// a password hash.
func (c *Client) ListUsers(ctx context.Context) ([]*schema.User, error) {
	var out struct {
		Users []*schema.User `json:"users"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/users", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Users, nil
}

// CreateUser creates one account (admin only). An empty password creates a
// passwordless user.
func (c *Client) CreateUser(ctx context.Context, username, password, workspace string, admin bool) (*schema.User, error) {
	var out struct {
		User *schema.User `json:"user"`
	}
	err := c.post(ctx, "/api/users", map[string]any{
		"username": username, "password": password, "workspace": workspace, "admin": admin,
	}, &out)
	if err != nil {
		return nil, err
	}
	return out.User, nil
}

// UpdateUser patches one account's password/workspace/admin (admin only).
func (c *Client) UpdateUser(ctx context.Context, username string, patch UserPatch) (*schema.User, error) {
	var out struct {
		User *schema.User `json:"user"`
	}
	err := c.do(ctx, http.MethodPut, "/api/users/"+url.PathEscape(username), nil, patch, &out)
	if err != nil {
		return nil, err
	}
	return out.User, nil
}

// DeleteUser deletes one account (admin only; the server rejects deleting
// the calling account itself). A missing username is a 404 *Error.
func (c *Client) DeleteUser(ctx context.Context, username string) error {
	if username == "" {
		return fmt.Errorf("DeleteUser: empty username")
	}
	return c.do(ctx, http.MethodDelete, "/api/users/"+url.PathEscape(username), nil, nil, nil)
}
