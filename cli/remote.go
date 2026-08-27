// remote.go implements the CLI's --server mode: instead of opening a local
// engine, the data commands call a `ladym serve --http` data-plane (the api
// package) over HTTP. The HTTP half is a thin shell over the Go SDK
// (client/golang) — this file keeps only the CLI-specific parts: flag/env
// credential resolution, per-command timeouts, and translation of SDK errors
// into the CLI's historical single-line messages. Stdout stays byte-identical
// to the local path — the commands share the same print helpers; only the
// "fetch the result" half differs.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	client "github.com/ProjAnvil/LadyM/client/golang"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/spf13/cobra"
)

// Remote request timeouts: consolidate runs System2 cycles and can take far
// longer than a plain read/write.
const (
	remoteDefaultTimeout     = 30 * time.Second
	remoteConsolidateTimeout = 120 * time.Second
)

// remoteClient talks to one `ladym serve --http` server through the SDK.
type remoteClient struct {
	baseURL string // scheme://host:port, no trailing slash (for error texts)
	sdk     *client.Client
}

func newRemoteClient(baseURL string, auth remoteAuth) *remoteClient {
	base := strings.TrimRight(baseURL, "/")
	return &remoteClient{baseURL: base, sdk: client.New(base, client.WithAuth(auth.user, auth.password))}
}

// remoteAuth is a resolved Basic-auth credential pair.
type remoteAuth struct{ user, password string }

// resolveRemoteAuth: --user/--password flags > LADYM_USER/LADYM_PASSWORD env
// > "" (no auth). A username with an empty password is valid — it matches a
// passwordless server account.
func resolveRemoteAuth(flagUser, flagPassword string) remoteAuth {
	user := flagUser
	if user == "" {
		user = os.Getenv("LADYM_USER")
	}
	password := flagPassword
	if password == "" {
		password = os.Getenv("LADYM_PASSWORD")
	}
	return remoteAuth{user, password}
}

// addRemoteFlags wires --server/--user/--password onto one data command.
// Local flags (not a root persistent flag) so the commands keep working when
// constructed standalone, as the test harness does.
func addRemoteFlags(cmd *cobra.Command, server, user, password *string) {
	cmd.Flags().StringVar(server, "server", "", "URL of a remote ladym server (e.g. http://127.0.0.1:8080) — run against `ladym serve --http` instead of the local db")
	cmd.Flags().StringVar(user, "user", "", "Username for --server Basic auth (default: LADYM_USER env)")
	cmd.Flags().StringVar(password, "password", "", "Password for --server Basic auth (default: LADYM_PASSWORD env)")
}

// remoteGuard enforces --db / --server exclusivity: with --server the remote
// server owns the database, so a local --db path would be silently ignored.
func remoteGuard(db, server string) error {
	if server != "" && db != "" {
		return &config.ConfigError{Msg: "--db and --server are mutually exclusive: --server delegates storage to the remote ladym server; drop --db"}
	}
	return nil
}

// translateErr maps an SDK error onto the CLI's historical single-line
// *config.ConfigError texts (pinned by remote_test.go): server rejections
// carry the server's error field, context deadlines the timeout line, and
// network/transport failures pass through — the SDK already formats those
// exactly as the CLI did ("cannot reach ladym server at %s: %v" et al).
func (c *remoteClient) translateErr(ctx context.Context, timeout time.Duration, err error) error {
	var apiErr *client.Error
	if errors.As(err, &apiErr) {
		return &config.ConfigError{Msg: fmt.Sprintf("ladym server at %s: %s", c.baseURL, apiErr.Message)}
	}
	if ctx.Err() == context.DeadlineExceeded {
		return &config.ConfigError{Msg: fmt.Sprintf("ladym server at %s did not respond within %s", c.baseURL, timeout)}
	}
	return &config.ConfigError{Msg: err.Error()}
}

// callRemote runs fn with a timeout-bound context and translates errors.
func callRemote[T any](rc *remoteClient, timeout time.Duration, fn func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	res, err := fn(ctx)
	if err != nil {
		var zero T
		return zero, rc.translateErr(ctx, timeout, err)
	}
	return res, nil
}

// callRemoteErr is callRemote for result-free calls (forget).
func callRemoteErr(rc *remoteClient, timeout time.Duration, fn func(context.Context) error) error {
	_, err := callRemote(rc, timeout, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn(ctx)
	})
	return err
}

// ---- one method per command (thin wrappers over the SDK) ----

func (c *remoteClient) remember(content, source string, tags []string, workspace string) (*client.RememberResult, error) {
	return callRemote(c, remoteDefaultTimeout, func(ctx context.Context) (*client.RememberResult, error) {
		return c.sdk.RememberWithSource(ctx, content, source, tags, workspace)
	})
}

func (c *remoteClient) recordEvent(agent, action, observation, outcome string, tags []string, workspace string) (*client.RecordEventResult, error) {
	return callRemote(c, remoteDefaultTimeout, func(ctx context.Context) (*client.RecordEventResult, error) {
		return c.sdk.RecordEvent(ctx, agent, action, observation, outcome, tags, workspace)
	})
}

// recall hits /api/recall (code_only=true covers the local --code path, which
// the server maps to SearchCode).
func (c *remoteClient) recall(query string, topK int, codeOnly bool, workspace string) (*schema.RecallResponse, error) {
	return callRemote(c, remoteDefaultTimeout, func(ctx context.Context) (*schema.RecallResponse, error) {
		return c.sdk.Recall(ctx, query, client.RecallOptions{Workspace: workspace, TopK: topK, CodeOnly: codeOnly})
	})
}

func (c *remoteClient) stats(workspace string) (*schema.Stats, error) {
	return callRemote(c, remoteDefaultTimeout, func(ctx context.Context) (*schema.Stats, error) {
		return c.sdk.Stats(ctx, workspace)
	})
}

func (c *remoteClient) forget(memoryID string) error {
	return callRemoteErr(c, remoteDefaultTimeout, func(ctx context.Context) error {
		return c.sdk.Forget(ctx, memoryID)
	})
}

func (c *remoteClient) link(src, dst, relation string) (string, error) {
	return callRemote(c, remoteDefaultTimeout, func(ctx context.Context) (string, error) {
		return c.sdk.Link(ctx, src, dst, relation)
	})
}

func (c *remoteClient) consolidate(workspace string) (*client.ConsolidateResult, error) {
	return callRemote(c, remoteConsolidateTimeout, func(ctx context.Context) (*client.ConsolidateResult, error) {
		return c.sdk.Consolidate(ctx, workspace, 0)
	})
}

// ---- shared print helpers (local and remote paths print identically) ----

// printRemembered mirrors the local remember output, including the
// gated-drop line.
func printRemembered(id, hash, gated, reason string) {
	if gated == "dropped" {
		fmt.Printf("dropped reason=%s (gated; not persisted)\n", reason)
	} else {
		fmt.Printf("remembered id=%s hash=%s\n", id, shortHash(hash))
	}
}

func printRecorded(id, layer, typ string) {
	fmt.Printf("recorded id=%s layer=%s type=%s\n", id, layer, typ)
}

// printRecall emits the recall output (table or JSON payload); shared by the
// local engine path and the remote path.
func printRecall(resp *schema.RecallResponse, jsonOut bool) error {
	if jsonOut {
		payload := map[string]any{
			"query": resp.Query, "tier_reached": resp.TierReached,
			"reflected_sufficient": resp.ReflectedSufficient, "elapsed_ms": resp.ElapsedMs,
			"results": recallResultsJSON(resp.Results),
		}
		b, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(resp.Results) == 0 {
		fmt.Println("no memories matched")
		return nil
	}
	fmt.Printf("recall: %s  (tier %d, %.1fms)\n", resp.Query, resp.TierReached, resp.ElapsedMs)
	writeRecallTable(os.Stdout, resp)
	return nil
}

func printLinked(src, relation, dst, edgeID string) {
	fmt.Printf("linked %s -[%s]-> %s (id=%s)\n", src, relation, dst, edgeID)
}

func printConsolidated(keptEpisodes int, actions map[string]int) {
	fmt.Printf("consolidated %d episodes: ADD=%d UPDATE=%d DELETE=%d NOOP=%d\n",
		keptEpisodes, actions["ADD"], actions["UPDATE"], actions["DELETE"], actions["NOOP"])
}
