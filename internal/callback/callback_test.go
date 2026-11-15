// SPDX-License-Identifier: MIT

package callback

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// lines is a Logger that keeps its entries.
type lines []string

func (l *lines) Errorf(format string, args ...any) { *l = append(*l, fmt.Sprintf(format, args...)) }

// endsAbnormally is application code that does not return normally.
func endsAbnormally() { panic(errors.New("ended by the test")) }

func TestTry(t *testing.T) {
	ran := false
	if err := Try("Normal", func() { ran = true }); err != nil || !ran {
		t.Fatalf("a function that returns normally: %v, ran %t", err, ran)
	}
	err := Try("Config.OnDERControl", endsAbnormally)
	if err == nil {
		t.Fatal("a function that does not return normally gives no error")
	}
	if err.Callback != "Config.OnDERControl" || fmt.Sprint(err.Value) != "ended by the test" {
		t.Errorf("error %#v", err)
	}
	if want := "Config.OnDERControl did not return normally: ended by the test"; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
	if !strings.Contains(string(err.stack), "endsAbnormally") {
		t.Errorf("the stack does not name the function:\n%s", err.stack)
	}
}

func TestDoAndReport(t *testing.T) {
	var l lines
	if err := Do(&l, "Normal", func() {}); err != nil || len(l) != 0 {
		t.Fatalf("a function that returns normally: %v, entries %q", err, l)
	}
	if err := Do(&l, "Metrics.OnPoll", endsAbnormally); err == nil {
		t.Fatal("no error")
	}
	if len(l) != 1 || !strings.HasPrefix(l[0], "Metrics.OnPoll did not return normally: ended by the test\n") ||
		!strings.Contains(l[0], "endsAbnormally") {
		t.Fatalf("entries %q", l)
	}
	Report(&l, nil)
	Report(nil, Try("x", endsAbnormally))
	if len(l) != 1 {
		t.Errorf("Report of nothing logged: %q", l)
	}
	// A Logger whose Errorf does not return normally loses the entry, and
	// nothing reaches the caller.
	Report(failing{}, Try("x", endsAbnormally))
}

type failing struct{}

func (failing) Errorf(string, ...any) { panic("Errorf of the test") }

// measured is application code whose outcome the library does not use.
func measured(log Logger, f func()) {
	defer Rescue(log, "Metrics.OnEvent")
	f()
}

func TestRescue(t *testing.T) {
	var l lines
	measured(&l, func() {})
	if len(l) != 0 {
		t.Fatalf("a function that returns normally: entries %q", l)
	}
	measured(&l, endsAbnormally)
	if len(l) != 1 || !strings.HasPrefix(l[0], "Metrics.OnEvent did not return normally: ended by the test\n") ||
		!strings.Contains(l[0], "endsAbnormally") {
		t.Fatalf("entries %q", l)
	}
	// Without a Logger, and with one that does not return normally, the
	// caller goes on all the same.
	measured(nil, endsAbnormally)
	measured(failing{}, endsAbnormally)
}

// levels is a LevelLogger that keeps its entries with their level, and
// does not return normally from an entry that says so.
type levels []string

func (l *levels) add(level, format string, args ...any) {
	if strings.Contains(format, "fail") {
		panic("the Logger of the test")
	}
	*l = append(*l, level+" "+fmt.Sprintf(format, args...))
}

func (l *levels) Debugf(format string, args ...any) { l.add("DEBUG", format, args...) }
func (l *levels) Infof(format string, args ...any)  { l.add("INFO", format, args...) }
func (l *levels) Warnf(format string, args ...any)  { l.add("WARN", format, args...) }
func (l *levels) Errorf(format string, args ...any) { l.add("ERROR", format, args...) }

func TestGuard(t *testing.T) {
	if Guard(nil) != nil {
		t.Fatal("Guard(nil) is not nil")
	}
	var l levels
	g := Guard(&l)
	g.Debugf("a %d", 1)
	g.Infof("b %d", 2)
	g.Warnf("c %d", 3)
	g.Errorf("d %d", 4)
	// Each of these entries is lost, and the caller goes on.
	g.Debugf("fail")
	g.Infof("fail")
	g.Warnf("fail")
	g.Errorf("fail")
	g.Infof("e")
	if got, want := strings.Join(l, "|"), "DEBUG a 1|INFO b 2|WARN c 3|ERROR d 4|INFO e"; got != want {
		t.Fatalf("entries %q, want %q", got, want)
	}
}
