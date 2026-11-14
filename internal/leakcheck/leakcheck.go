// SPDX-License-Identifier: MIT

// Package leakcheck finds goroutines that tests leave behind. Only test
// code imports it.
//
// It compares goroutine identifiers from stack dumps, not counts: Take
// records the goroutines that exist, and Snapshot.Leaked returns every
// goroutine started since that still exists, with its stack. There is no
// tolerance. Goroutines that are ending get until a deadline to end; the
// check passes as soon as none is left.
//
// Main runs a package's tests and then the check, for TestMain; Check does
// the same around one test. WaitBlocked lets a test wait until a goroutine
// has reached a wait, instead of sleeping.
package leakcheck

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Wait is how long Main and Check give goroutines started by the tests to
// end before they are reported.
const Wait = 10 * time.Second

// Goroutine is one goroutine of a stack dump.
type Goroutine struct {
	ID    uint64
	State string // "chan receive", "select", "IO wait", ...
	Stack string // its part of the dump, header included
}

// Snapshot is the set of goroutines that existed when it was taken.
type Snapshot struct {
	ids map[uint64]bool
}

// Take records the goroutines that exist now.
func Take() Snapshot {
	all := current()
	ids := make(map[uint64]bool, len(all))
	for _, g := range all {
		ids[g.ID] = true
	}
	return Snapshot{ids: ids}
}

// Leaked returns the goroutines started after s that still exist, other
// than the caller. It looks again until none is left or wait has passed,
// so that goroutines that are ending are not reported.
func (s Snapshot) Leaked(wait time.Duration) []Goroutine {
	deadline := time.Now().Add(wait)
	pause := time.Millisecond
	for {
		leaked := s.newer()
		if len(leaked) == 0 || !time.Now().Before(deadline) {
			return leaked
		}
		// Waiting for goroutines that are ending: the outcome depends on
		// what is left at the deadline, not on the pause.
		time.Sleep(pause)
		pause = min(2*pause, 100*time.Millisecond)
	}
}

// newer returns the goroutines of a dump taken now that are not in s, not
// the caller and not kept by the standard library or the test framework.
func (s Snapshot) newer() []Goroutine {
	var leaked []Goroutine
	for i, g := range current() {
		if i == 0 || s.ids[g.ID] || ignored(g) { // the dump lists the caller first
			continue
		}
		leaked = append(leaked, g)
	}
	return leaked
}

// ignored reports the goroutines the standard library keeps for the life
// of the process once a feature is used, and those of the fuzzing engine.
func ignored(g Goroutine) bool {
	for _, frame := range []string{"os/signal.loop", "testing.runFuzzing", "internal/fuzz."} {
		if strings.Contains(g.Stack, "\n"+frame) {
			return true
		}
	}
	return false
}

// Report formats goroutines for a test failure.
func Report(gs []Goroutine) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d goroutine(s) started by the tests are still running:\n", len(gs))
	for _, g := range gs {
		b.WriteString("\n" + g.Stack + "\n")
	}
	return b.String()
}

// Runner is what Main runs: a *testing.M.
type Runner interface {
	Run() int
}

// Main runs the tests of m and, when they pass, fails the run if a
// goroutine they started is still running after Wait. It returns the exit
// code for os.Exit:
//
//	func TestMain(m *testing.M) { os.Exit(leakcheck.Main(m)) }
func Main(m Runner) int {
	return run(m, Wait)
}

func run(m Runner, wait time.Duration) int {
	base := Take()
	code := m.Run()
	if code != 0 {
		return code
	}
	if leaked := base.Leaked(wait); len(leaked) > 0 {
		_, _ = fmt.Fprintln(stderr, "leakcheck: "+Report(leaked))
		return 1
	}
	return 0
}

// stderr receives the report of Main; a variable for the package's tests.
var stderr io.Writer = os.Stderr

// TB is the part of testing.TB that Check uses.
type TB interface {
	Helper()
	Cleanup(func())
	Errorf(format string, args ...any)
	Failed() bool
}

// Check takes a snapshot and, unless t has failed, fails t at cleanup if a
// goroutine started since is still running after Wait. Call it first in a
// test that does not run in parallel with others: its cleanup then runs
// after the cleanups the test registers later, Close included.
func Check(t TB) {
	t.Helper()
	check(t, Wait)
}

func check(t TB, wait time.Duration) {
	base := Take()
	t.Cleanup(func() {
		if t.Failed() {
			return
		}
		if leaked := base.Leaked(wait); len(leaked) > 0 {
			t.Errorf("%s", Report(leaked))
		}
	})
}

// WaitBlocked reports, within wait, that a goroutine is blocked (on a
// channel, a select, a lock or I/O) directly in the function frame, such
// as "discovery.(*browser).deliver": frame is the innermost function of
// its stack outside package runtime. Tests use it to know that a goroutine
// has reached the wait they mean to interrupt.
func WaitBlocked(frame string, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	pause := 100 * time.Microsecond
	for {
		for _, g := range current()[1:] {
			if blocked(g.State) && strings.Contains(innermost(g.Stack), frame) {
				return true
			}
		}
		if !time.Now().Before(deadline) {
			return false
		}
		// Looking again for the state: the result is the state seen, not
		// the pause.
		time.Sleep(pause)
		pause = min(2*pause, 10*time.Millisecond)
	}
}

// innermost returns the first function line of a goroutine's stack that is
// not in package runtime.
func innermost(stack string) string {
	lines := strings.Split(stack, "\n")
	for _, l := range lines[1:] { // lines[0] is the header
		if l == "" || l[0] == '\t' || strings.HasPrefix(l, "runtime.") {
			continue
		}
		return l
	}
	return ""
}

// blocked reports whether a goroutine state is a wait: not running,
// runnable or in a system call.
func blocked(state string) bool {
	state, _, _ = strings.Cut(state, ",") // "select, 2 minutes"
	switch state {
	case "", "running", "runnable", "syscall":
		return false
	}
	return true
}

// current returns every goroutine of the process, the caller first.
func current() []Goroutine {
	buf := make([]byte, bufSize)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return parse(buf[:n])
		}
		buf = make([]byte, 2*len(buf))
	}
}

// bufSize is the size of the first buffer for a dump; a variable for the
// package's tests.
var bufSize = 64 << 10

// parse splits a runtime.Stack dump into goroutines. Each starts with a
// header line "goroutine 12 [chan receive]:" and ends at a blank line.
func parse(dump []byte) []Goroutine {
	var out []Goroutine
	for _, block := range bytes.Split(dump, []byte("\n\n")) {
		block = bytes.TrimSpace(block)
		header, _, _ := bytes.Cut(block, []byte("\n"))
		rest, ok := bytes.CutPrefix(header, []byte("goroutine "))
		if !ok {
			continue
		}
		idText, _, _ := bytes.Cut(rest, []byte(" "))
		id, err := strconv.ParseUint(string(idText), 10, 64)
		if err != nil {
			continue
		}
		var state string
		if open := bytes.IndexByte(rest, '['); open >= 0 {
			if end := bytes.IndexByte(rest[open:], ']'); end > 0 {
				state = string(rest[open+1 : open+end])
			}
		}
		out = append(out, Goroutine{ID: id, State: state, Stack: string(block)})
	}
	return out
}
