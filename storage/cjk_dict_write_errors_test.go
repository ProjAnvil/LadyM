package storage

// Error paths of the CJK dictionary download/write pipeline: explicit mirror
// override, destination-dir creation failure, read-only destination,
// manifest rename collision, truncated mirror responses, empty mirror list,
// and atomicWrite's own failure modes.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDownloadCJKDictToExplicitMirrorBase: a non-empty mirrorBase replaces
// the configured mirror list (the air-gapped internal-mirror path).
func TestDownloadCJKDictToExplicitMirrorBase(t *testing.T) {
	fixtureRegistry(t, "用户 1024 n\n", "資料庫 64 n\n", "テスト 1 n\n")
	dir := t.TempDir()
	setCJKDictDir(t, dir)

	st, err := DownloadCJKDictTo(CJKDictZH, dir, cjkDictMirrorFn()[0])
	if err != nil {
		t.Fatal(err)
	}
	if st.Source != "file" || st.Variant != CJKDictZH {
		t.Errorf("status = %+v, want file/zh via explicit mirror base", st)
	}
}

// TestDownloadCJKDictDirCreateError: the destination's parent is a regular
// file, so MkdirAll fails after the (successful) downloads.
func TestDownloadCJKDictDirCreateError(t *testing.T) {
	fixtureRegistry(t, "用户 1024 n\n", "資料庫 64 n\n", "テスト 1 n\n")
	blocker := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := DownloadCJKDictTo(CJKDictZH, filepath.Join(blocker, "dict"), "")
	if err == nil || !strings.Contains(err.Error(), "create dict dir") {
		t.Fatalf("err = %v, want create-dict-dir failure", err)
	}
}

// TestDownloadCJKDictReadOnlyDir: the destination directory exists but is
// not writable, so the first dict file's atomicWrite fails.
func TestDownloadCJKDictReadOnlyDir(t *testing.T) {
	fixtureRegistry(t, "用户 1024 n\n", "資料庫 64 n\n", "テスト 1 n\n")
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) }) // so TempDir cleanup can remove it

	_, err := DownloadCJKDictTo(CJKDictZH, dir, "")
	if err == nil || !strings.Contains(err.Error(), "write") {
		t.Fatalf("err = %v, want atomicWrite failure", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "s_1.txt")); !os.IsNotExist(statErr) {
		t.Error("no dict file may be left behind in the read-only dir")
	}
}

// TestDownloadCJKDictManifestRenameCollision: manifest.json already exists
// as a directory, so the final atomicWrite fails at rename — after the dict
// files themselves landed (coherent partial state, error surfaced).
func TestDownloadCJKDictManifestRenameCollision(t *testing.T) {
	fixtureRegistry(t, "用户 1024 n\n", "資料庫 64 n\n", "テスト 1 n\n")
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "manifest.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := DownloadCJKDictTo(CJKDictZH, dir, "")
	if err == nil || !strings.Contains(err.Error(), "manifest.json") {
		t.Fatalf("err = %v, want manifest write failure", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "s_1.txt")); statErr != nil {
		t.Error("dict files are written before the manifest, so s_1.txt should exist")
	}
}

// TestDownloadDictFileTruncatedBody: the mirror closes the connection
// mid-body, so ReadAll fails and the download reports a read error.
func TestDownloadDictFileTruncatedBody(t *testing.T) {
	fixtureRegistry(t, "用户 1024 n\n", "資料庫 64 n\n", "テスト 1 n\n")
	truncated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		// Advertise a large body, send a short one, then close: the client's
		// ReadAll sees an unexpected EOF.
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 1048576\r\nContent-Type: text/plain\r\n\r\nshort")
		_ = buf.Flush()
	}))
	t.Cleanup(truncated.Close)
	prevMirror := cjkDictMirrorFn
	cjkDictMirrorFn = func() []string { return []string{truncated.URL + "/"} }
	t.Cleanup(func() { cjkDictMirrorFn = prevMirror })

	_, err := DownloadCJKDictTo(CJKDictZH, t.TempDir(), "")
	if err == nil || !strings.Contains(err.Error(), "read body") {
		t.Fatalf("err = %v, want read-body failure", err)
	}
}

// TestDownloadDictFileNoMirrorsConfigured: with an empty mirror list the
// per-file loop never runs and the fallback "no mirrors" error fires.
func TestDownloadDictFileNoMirrorsConfigured(t *testing.T) {
	fixtureRegistry(t, "用户 1024 n\n", "資料庫 64 n\n", "テスト 1 n\n")
	prevMirror := cjkDictMirrorFn
	cjkDictMirrorFn = func() []string { return nil }
	t.Cleanup(func() { cjkDictMirrorFn = prevMirror })

	_, err := DownloadCJKDictTo(CJKDictZH, t.TempDir(), "")
	if err == nil || !strings.Contains(err.Error(), "no mirrors configured") {
		t.Fatalf("err = %v, want no-mirrors failure", err)
	}
}

// TestAtomicWriteErrors exercises atomicWrite's failure modes directly:
// a missing parent dir breaks CreateTemp, an existing directory at the
// target path breaks the final rename, and a plain write round-trips.
func TestAtomicWriteErrors(t *testing.T) {
	if err := atomicWrite(filepath.Join(t.TempDir(), "missing", "f.txt"), []byte("x")); err == nil {
		t.Error("atomicWrite with a missing parent dir should fail at CreateTemp")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(target, []byte("x")); err == nil {
		t.Error("atomicWrite onto an existing directory should fail at rename")
	}

	ok := filepath.Join(dir, "ok.txt")
	if err := atomicWrite(ok, []byte("payload")); err != nil {
		t.Fatalf("atomicWrite happy path: %v", err)
	}
	body, err := os.ReadFile(ok)
	if err != nil || string(body) != "payload" {
		t.Errorf("read back = %q, %v; want payload", body, err)
	}
}
