// Tests for the password-resolution helpers behind `user add`/`user passwd`:
// the second entry of the two-prompt read failing, the real TTY probe, the
// interactive path reading piped stdin when a TTY is present, and the empty
// (passwordless) hash shortcut.

package cli

import (
	"errors"
	"os"
	"testing"
)

// secondFailReader serves the first read from data, then fails — covering the
// confirm-prompt read error in readPasswordTwice.
type secondFailReader struct {
	data []byte
	done bool
}

func (r *secondFailReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errors.New("read exploded on second entry")
	}
	r.done = true
	return copy(p, r.data), nil
}

// A failure reading the confirm entry propagates instead of comparing a
// partial password.
func TestReadPasswordTwice_SecondReadError_Propagates(t *testing.T) {
	r := &secondFailReader{data: []byte("s3cret\n")}
	if _, err := readPasswordTwice(r, nil); err == nil ||
		err.Error() != "read exploded on second entry" {
		t.Errorf("second-read failure: err = %v, want the reader's error", err)
	}
}

// The real stdinIsTTY probe just stats stdin; it must be safe to call and
// deterministic within a process.
func TestStdinIsTTY_RealStdin_Deterministic(t *testing.T) {
	if stdinIsTTY() != stdinIsTTY() {
		t.Error("stdinIsTTY returned different results on consecutive calls")
	}
}

// With a TTY (forced via the test hook) and no --password-env, the password
// comes from the interactive two-prompt read on stdin.
func TestResolveNewPassword_TTY_ReadsInteractivePrompt(t *testing.T) {
	oldTTY := stdinIsTTY
	stdinIsTTY = func() bool { return true }
	t.Cleanup(func() { stdinIsTTY = oldTTY })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("tty-pw\ntty-pw\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldStdin })

	pw, err := resolveNewPassword("")
	if err != nil || pw != "tty-pw" {
		t.Errorf("TTY prompt path: pw=%q err=%v, want tty-pw", pw, err)
	}
}

// hashPassword keeps "" as "" (passwordless user) instead of bcrypting it.
func TestHashPassword_Empty_StaysEmpty(t *testing.T) {
	h, err := hashPassword("")
	if err != nil || h != "" {
		t.Errorf("hashPassword(\"\") = %q, %v; want empty, nil", h, err)
	}
}
