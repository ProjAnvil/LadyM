// Tests for the remote (--server) error paths that the happy-path remote
// tests skip: each command surfaces the server's rejection as a single-line
// error, and translateErr maps an expired context onto the timeout line.

package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// Every remote-capable command surfaces a server-side 500 as a single-line
// error carrying the server's message.
func TestRemoteCmds_ServerError_PropagatesMessage(t *testing.T) {
	srv, _ := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"store exploded"}`)
	})

	cases := []struct {
		name string
		cmd  func() *cobra.Command
		args []string
	}{
		{"remember", rememberCmd, []string{"a fact"}},
		{"record", recordCmd, []string{"--agent", "a", "--action", "b"}},
		{"consolidate", consolidateCmd, nil},
		{"forget", forgetCmd, []string{"mem-1"}},
		{"link", linkCmd, []string{"mem-1", "mem-2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runCmd(t, tc.cmd(), append([]string{"--server", srv.URL}, tc.args...)...)
			if err == nil {
				t.Fatalf("%s: expected error for 500", tc.name)
			}
			if !strings.Contains(err.Error(), "store exploded") {
				t.Errorf("%s: error = %q, want the server's message", tc.name, err.Error())
			}
			if strings.Contains(err.Error(), "\n") {
				t.Errorf("%s: error must be a single line: %q", tc.name, err.Error())
			}
		})
	}
}

// translateErr: when the request context hit its deadline and the SDK error
// is not a server rejection, the message names the timeout instead of the
// transport detail.
func TestTranslateErr_DeadlineExceeded_ReportsTimeout(t *testing.T) {
	rc := newRemoteClient("http://127.0.0.1:1", remoteAuth{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond) // let the deadline pass

	err := rc.translateErr(ctx, remoteDefaultTimeout, errors.New("connection reset"))
	if err == nil {
		t.Fatal("expected an error")
	}
	want := fmt.Sprintf("ladym server at http://127.0.0.1:1 did not respond within %s", remoteDefaultTimeout)
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}
