//go:build !windows

package storage

// atomicWrite's temp-file write failure: RLIMIT_FSIZE caps any single file
// in the process at 1 byte, so CreateTemp succeeds but tmp.Write of a larger
// payload fails with EFBIG. SIGXFSZ (the kernel's companion signal, whose
// default action terminates the process) is ignored for the duration, and
// both the signal disposition and the limit are restored afterwards. Storage
// tests do not run in parallel, so the process-wide limit is safe.
//
// The remaining atomicWrite failure modes (tmp.Close and os.Chmod after a
// successful write) are defensive-only: an fd just written to and still open
// cannot be made to fail Close or Chmod from Go.

import (
	"os/signal"
	"strings"
	"syscall"
	"testing"
)

func TestAtomicWriteWriteErrorUnderFilesizeLimit(t *testing.T) {
	signal.Ignore(syscall.SIGXFSZ)
	t.Cleanup(func() { signal.Reset(syscall.SIGXFSZ) })

	var old syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &old); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 1, Max: old.Max}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &old); err != nil {
			t.Fatalf("restore RLIMIT_FSIZE: %v", err)
		}
	})

	err := atomicWrite(t.TempDir()+"/dict.txt", []byte("payload too large for a 1-byte limit"))
	if err == nil || !strings.Contains(err.Error(), "file too large") {
		t.Fatalf("atomicWrite under RLIMIT_FSIZE = %v, want EFBIG write failure", err)
	}
}
