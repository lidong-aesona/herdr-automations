package host

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// hwfWork hands the run to herdr-workflows and waits for its verdict.
//
// A pane has no exit code to return, so the command is asked to print one
// behind a marker and the screen is read back for it. Without that, launching
// the command successfully was indistinguishable from the workflow succeeding,
// and a `workflow:` automation reported done whatever happened — including
// when hwf was not installed at all.
type hwfWork struct {
	ops   ops
	knobs knobs
	name  string
}

func (w hwfWork) do(s Session, timeout time.Duration) error {
	if err := w.ops.LookPath("hwf"); err != nil {
		return fmt.Errorf("workflow %q needs herdr-workflows: hwf is not on PATH", w.name)
	}
	code, err := runMarked(w.ops, w.knobs, s, "hwf run "+shellQuote(w.name), timeout)
	switch {
	case errors.Is(err, errStillRunning):
		return fmt.Errorf("workflow %s did not finish within %s", w.name, timeout)
	case err != nil:
		return fmt.Errorf("running workflow %s: %w", w.name, err)
	case code != 0:
		return fmt.Errorf("workflow %s exited %d", w.name, code)
	}
	return nil
}

// commandWork runs a command: line in the pane, read back the same way.
type commandWork struct {
	ops     ops
	knobs   knobs
	command string
}

func (w commandWork) do(s Session, timeout time.Duration) error {
	code, err := runMarked(w.ops, w.knobs, s, w.command, timeout)
	switch {
	case errors.Is(err, errStillRunning):
		return fmt.Errorf("the command was still running after %s", timeout)
	case err != nil:
		return fmt.Errorf("running the command: %w", err)
	case code != 0:
		return fmt.Errorf("the command exited with code %d", code)
	}
	return nil
}

var errStillRunning = errors.New("still running")

// runMarked runs line through sh -c in the pane and returns its exit status,
// or errStillRunning once timeout passes without one.
func runMarked(o ops, k knobs, s Session, line string, timeout time.Duration) (int, error) {
	marker := fmt.Sprintf("HWF-%d", time.Now().UnixNano())
	// The shell echoes the command it was given, so the marker appears twice on
	// screen: once as this literal (with %d unexpanded) and once with the real
	// status. Only the latter matches a digit, which is what exitCode looks for.
	// The line runs in a subshell so an exit in it still reaches the printf,
	// on lines of its own so a trailing comment cannot swallow the paren.
	command := fmt.Sprintf("(\n%s\n); printf '\\n%s:%%d\\n' $?", line, marker)
	if err := o.PaneRun(s.PaneID, "sh", "-c", command); err != nil {
		return 0, err
	}

	deadline := time.Now().Add(timeout)
	for {
		screen, err := o.PaneRead(s.PaneID, 200)
		if err == nil {
			if code, done := exitCode(screen, marker); done {
				return code, nil
			}
		}
		if time.Now().After(deadline) {
			return 0, errStillRunning
		}
		time.Sleep(k.workflowPoll)
	}
}

// exitCode finds the status the marker was printed with. The last match wins:
// a pane may hold output from an earlier run of the same automation.
func exitCode(screen, marker string) (int, bool) {
	matches := regexp.MustCompile(regexp.QuoteMeta(marker)+`:(\d+)`).FindAllStringSubmatch(screen, -1)
	if len(matches) == 0 {
		return 0, false
	}
	code, err := strconv.Atoi(matches[len(matches)-1][1])
	if err != nil {
		return 0, false
	}
	return code, true
}

// shellQuote makes a workflow name safe to interpolate into the sh -c string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
