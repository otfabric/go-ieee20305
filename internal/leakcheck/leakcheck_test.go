// SPDX-License-Identifier: MIT

package leakcheck

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// parkedForLeakcheckTest blocks until release is closed: the goroutine the
// tests leave running.
func parkedForLeakcheckTest(release <-chan struct{}) { <-release }

// startParked starts a parked goroutine and returns once it is blocked.
func startParked(t *testing.T, release <-chan struct{}) {
	t.Helper()
	go parkedForLeakcheckTest(release)
	if !WaitBlocked("leakcheck.parkedForLeakcheckTest", Wait) {
		t.Fatal("the parked goroutine did not block")
	}
}

// TestLeaked: a goroutine started after the snapshot is reported with its
// state and stack while it runs, and not once it has ended; goroutines of
// the snapshot and the caller are not reported.
func TestLeaked(t *testing.T) {
	base := Take()
	release := make(chan struct{})
	startParked(t, release)
	leaked := base.Leaked(0)
	if len(leaked) != 1 || leaked[0].State != "chan receive" || !strings.Contains(leaked[0].Stack, "parkedForLeakcheckTest") {
		t.Fatalf("Leaked = %s", Report(leaked))
	}
	if r := Report(leaked); !strings.Contains(r, "1 goroutine(s)") {
		t.Fatalf("Report = %q", r)
	}
	if again := Take().Leaked(0); len(again) != 0 {
		t.Fatalf("goroutines of the snapshot reported: %s", Report(again))
	}
	close(release)
	if leaked := base.Leaked(Wait); len(leaked) != 0 {
		t.Fatalf("after the goroutine ended: %s", Report(leaked))
	}
}

// TestLeakedWaitsForEnding: a goroutine that ends while Leaked looks again
// is not reported.
func TestLeakedWaitsForEnding(t *testing.T) {
	base := Take()
	release := make(chan struct{})
	startParked(t, release)
	// Released, and the releasing goroutine ended, while Leaked looks: the
	// result does not depend on when.
	var wg sync.WaitGroup
	wg.Go(func() { close(release) })
	if leaked := base.Leaked(Wait); len(leaked) != 0 {
		t.Fatalf("Leaked: %s", Report(leaked))
	}
	wg.Wait()
}

type fakeTB struct {
	cleanups []func()
	errs     []string
	failed   bool
}

func (f *fakeTB) Helper()                   {}
func (f *fakeTB) Cleanup(fn func())         { f.cleanups = append(f.cleanups, fn) }
func (f *fakeTB) Failed() bool              { return f.failed }
func (f *fakeTB) Errorf(s string, a ...any) { f.errs = append(f.errs, fmt.Sprintf(s, a...)) }
func (f *fakeTB) runCleanups() {
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
}

// TestCheck: the cleanup fails the test when a goroutine is left, passes
// when none is, and adds nothing to a test that already failed.
func TestCheck(t *testing.T) {
	t.Run("leak", func(t *testing.T) {
		f := &fakeTB{}
		check(f, 0)
		release := make(chan struct{})
		startParked(t, release)
		f.runCleanups()
		close(release)
		if len(f.errs) != 1 || !strings.Contains(f.errs[0], "parkedForLeakcheckTest") {
			t.Fatalf("errors = %q, want one naming the goroutine", f.errs)
		}
	})
	t.Run("clean", func(t *testing.T) {
		f := &fakeTB{}
		Check(f)
		f.runCleanups()
		if len(f.errs) != 0 {
			t.Fatalf("errors = %q", f.errs)
		}
	})
	t.Run("already failed", func(t *testing.T) {
		f := &fakeTB{failed: true}
		check(f, 0)
		release := make(chan struct{})
		startParked(t, release)
		f.runCleanups()
		close(release)
		if len(f.errs) != 0 {
			t.Fatalf("errors = %q", f.errs)
		}
	})
}

type fakeM struct {
	code int
	run  func()
}

func (m fakeM) Run() int {
	if m.run != nil {
		m.run()
	}
	return m.code
}

// TestMainCodes: Main keeps a failing code, passes a clean run and fails a
// passing run that leaves a goroutine, naming it.
func TestMainCodes(t *testing.T) {
	var out bytes.Buffer
	old := stderr
	stderr = &out
	defer func() { stderr = old }()
	if code := Main(fakeM{}); code != 0 {
		t.Fatalf("clean run: code %d, output %q", code, out.String())
	}
	release := make(chan struct{})
	defer close(release)
	m := fakeM{run: func() { startParked(t, release) }}
	if code := run(m, 0); code != 1 || !strings.Contains(out.String(), "parkedForLeakcheckTest") {
		t.Fatalf("leaking run: code %d, output %q", code, out.String())
	}
	out.Reset()
	if code := run(fakeM{code: 3}, 0); code != 3 || out.Len() != 0 {
		t.Fatalf("failing run: code %d, output %q", code, out.String())
	}
}

// TestWaitBlocked: a goroutine blocked in the frame is found; none is
// found once the wait has passed.
func TestWaitBlocked(t *testing.T) {
	release := make(chan struct{})
	startParked(t, release)
	close(release)
	if WaitBlocked("leakcheck.noSuchFrame", 20*time.Millisecond) {
		t.Fatal("found a goroutine in a frame that does not exist")
	}
}

func TestCurrentGrowsBuffer(t *testing.T) {
	old := bufSize
	bufSize = 16
	defer func() { bufSize = old }()
	if all := current(); len(all) == 0 || !strings.Contains(all[0].Stack, "TestCurrentGrowsBuffer") {
		t.Fatalf("current() = %d goroutines, first:\n%v", len(all), all)
	}
}

func TestParse(t *testing.T) {
	dump := "goroutine 7 [running]:\nmain.f()\n\n" +
		"goroutine 12 gp=0x1 m=nil [select, 2 minutes]:\nmain.g()\n\n" +
		"goroutine x [running]:\n\n" +
		"not a goroutine\n\n" +
		"goroutine 13 no state\nmain.h()\n"
	got := parse([]byte(dump))
	want := []struct {
		id    uint64
		state string
	}{{7, "running"}, {12, "select, 2 minutes"}, {13, ""}}
	if len(got) != len(want) {
		t.Fatalf("parse = %+v", got)
	}
	for i, w := range want {
		if got[i].ID != w.id || got[i].State != w.state {
			t.Errorf("goroutine %d = %d %q, want %d %q", i, got[i].ID, got[i].State, w.id, w.state)
		}
	}
}

func TestStates(t *testing.T) {
	for state, want := range map[string]bool{"": false, "running": false, "runnable": false, "syscall": false,
		"chan receive": true, "select, 2 minutes": true, "sync.Mutex.Lock": true, "IO wait": true} {
		if blocked(state) != want {
			t.Errorf("blocked(%q) = %t", state, !want)
		}
	}
	if innermost("goroutine 1 [select]:\nruntime.gopark(...)\n\t/x.go:1\nruntime.selectgo()\n\t/y.go:2\npkg.f()\n\t/z.go:3") != "pkg.f()" {
		t.Error("innermost does not skip the runtime frames")
	}
	if innermost("goroutine 1 [select]:\nruntime.gopark(...)") != "" {
		t.Error("innermost of a runtime-only stack")
	}
	if !ignored(Goroutine{Stack: "goroutine 3 [select]:\nos/signal.loop()"}) || ignored(Goroutine{Stack: "goroutine 3 [select]:\npkg.f()"}) {
		t.Error("ignored")
	}
}
