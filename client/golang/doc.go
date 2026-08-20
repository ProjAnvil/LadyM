// Package client is ladyM's Go SDK for the HTTP data-plane (`ladym serve
// --http`): one method per /api/* endpoint — the nine MCP-tool endpoints
// (remember/recall/record_event/search via recall code_only/consolidate/
// stats/link/forget, plus login) and the management-console CRUD for
// memories and users — with database-level Basic auth (users table).
//
// It is the first language client; other languages live alongside it under
// client/<lang>/ (e.g. client/python/); the wire contract is api/api.go and
// api/crud.go.
//
// Usage:
//
//	c := client.New("http://127.0.0.1:8080", client.WithAuth("alice", "s3cret"))
//	res, err := c.Remember(ctx, "the sky is blue", []string{"fact"}, "")
//
// The client sets no default timeout — callers bound each call with their
// own context (the CLI wraps 30s/120s; see cli/remote.go). Non-2xx responses
// surface as *Error{StatusCode, Message} with Message taken from the
// server's {"error": ...} body; network failures are single-line actionable
// errors ("cannot reach ladym server at ...").
package client
