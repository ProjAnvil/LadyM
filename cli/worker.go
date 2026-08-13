package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/operations"
)

// runWorkerLoop runs System2 cycles. In --once mode errors propagate (non-zero
// exit); in loop mode failures are logged and the loop continues.
func runWorkerLoop(eng *engine.Engine, once bool, interval int, workspace string) error {
	for {
		if once {
			_, err := operations.RunSystem2Cycle(eng, workspace)
			return err
		}
		if _, err := operations.RunSystem2Cycle(eng, workspace); err != nil {
			fmt.Fprintf(os.Stderr, "system2 CLI worker cycle failed; continuing: %v\n", err)
		}
		time.Sleep(time.Duration(interval) * time.Second)
	}
}
