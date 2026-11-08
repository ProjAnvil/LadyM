package hook

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ProjAnvil/LadyM/engine"
)

// ProjectIdentity uses a lexical absolute git root, or cwd outside a repository.
// Symlink spelling is preserved; no repository content is read.
func ProjectIdentity(cwd string) (root, workspace string, err error) {
	root, err = filepath.Abs(cwd)
	if err != nil {
		return "", "", fmt.Errorf("hook: absolute cwd: %w", err)
	}
	root = filepath.Clean(root)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// --show-cdup avoids git's physical --show-toplevel resolving symlinks.
	cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--show-cdup")
	// Identity follows cwd, independent of inherited Git discovery/config controls.
	cmd.Env = slices.DeleteFunc(os.Environ(), func(entry string) bool {
		return strings.HasPrefix(entry, "GIT_")
	})
	cmd.Env = append(cmd.Env, "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", "", fmt.Errorf("hook: git root lookup: %w", ctx.Err())
		}
		exit, ok := errors.AsType[*exec.ExitError](err)
		if !ok || !strings.Contains(string(exit.Stderr), "not a git repository") {
			return "", "", fmt.Errorf("hook: git root lookup for %q: %w", root, err)
		}
	} else {
		root = filepath.Clean(filepath.Join(root, strings.TrimSpace(string(out))))
	}
	return root, fmt.Sprintf("project:%x", sha256.Sum256([]byte(root))), nil
}

// ToCanonicalOperation preserves event mapping while making file identities
// root-relative (or absolute outside root), with case-sensitive path resources.
func (p *Payload) ToCanonicalOperation(harness, root string) (*engine.OperationEvent, error) {
	ev := p.ToOperation(harness)
	if ev == nil {
		return nil, nil
	}
	ev.Metadata["resource_identity"] = "path"
	if p.HookEventName != "PostToolUse" && p.HookEventName != "PostToolUseFailure" {
		return ev, nil
	}
	if root == "" {
		return nil, errors.New("hook: canonical project root is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("hook: absolute project root: %w", err)
	}
	cwd := root
	if p.Cwd != "" {
		cwd, err = filepath.Abs(p.Cwd)
		if err != nil {
			return nil, fmt.Errorf("hook: absolute payload cwd: %w", err)
		}
	}
	ev.Resources = nil
	for _, key := range []string{"file_path", "notebook_path"} {
		name, ok := p.ToolInput[key].(string)
		if !ok || name == "" {
			continue
		}
		if strings.ContainsRune(name, '\x00') {
			return nil, errors.New("hook: resource path contains NUL")
		}
		if !filepath.IsAbs(name) {
			name = filepath.Join(cwd, name)
		}
		name = filepath.Clean(name)
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return nil, fmt.Errorf("hook: relative resource path: %w", err)
		}
		if relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			name = relative
		}
		ev.Resources = append(ev.Resources, name)
	}
	return ev, nil
}
