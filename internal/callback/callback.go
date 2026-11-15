// SPDX-License-Identifier: MIT

// Package callback is how the packages of this module call the
// application's code: a handler, a metrics hook or another function of the
// application that does not return normally is stopped where the library
// calls it, and the library goes on as it does when that code returns an
// error. The failure is logged at Error level, with what the code ended
// with and its stack, once the library holds none of its locks. The
// application's Logger is called through Guard: an entry it does not
// return normally from is lost.
package callback

import (
	"fmt"
	"runtime/debug"
)

// Logger is where a failure is logged: an ieee20305.Logger.
type Logger interface {
	Errorf(format string, args ...any)
}

// Error is a call of the application's code that did not return normally.
type Error struct {
	// Callback names the code, such as "Config.OnDERControl".
	Callback string
	// Value is what the code ended with.
	Value any
	stack []byte
}

// Error describes the call: "Config.OnDERControl did not return normally:
// <value>".
func (e *Error) Error() string {
	return fmt.Sprintf("%s did not return normally: %v", e.Callback, e.Value)
}

// Try calls f, the application's code named name, and returns nil, or the
// *Error of f when it does not return normally. It logs nothing: a caller
// that holds a lock gives the error to Report once it has released it.
func Try(name string, f func()) (err *Error) {
	defer func() {
		if r := recover(); r != nil {
			err = &Error{Callback: name, Value: r, stack: debug.Stack()}
		}
	}()
	f()
	return nil
}

// Do is Try for a caller that holds no lock: a failure is reported to log
// at once. It returns the *Error, or nil.
func Do(log Logger, name string, f func()) *Error {
	err := Try(name, f)
	Report(log, err)
	return err
}

// Report logs err at Error level with its stack; a nil err or log logs
// nothing. A Logger whose Errorf does not return normally loses the entry.
func Report(log Logger, err *Error) {
	if err == nil || log == nil {
		return
	}
	defer lose()
	log.Errorf("%v\n%s", err, err.stack)
}

// Rescue is deferred by a caller that holds no lock, around a call of the
// application's code named name whose outcome the library does not use,
// such as a method of a Metrics: when the code does not return normally,
// Rescue stops that there and reports it to log, as Do does.
//
//	defer callback.Rescue(log, "Metrics.OnPoll")
//	m.OnPoll(d, err)
func Rescue(log Logger, name string) {
	if r := recover(); r != nil {
		Report(log, &Error{Callback: name, Value: r, stack: debug.Stack()})
	}
}

// lose is deferred around a call of a Logger: an entry whose call does not
// return normally is lost, since there is nowhere to report it.
func lose() { _ = recover() }

// LevelLogger is the Logger of the application: an ieee20305.Logger.
type LevelLogger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

// Guard returns l with every method stopped where it does not return
// normally: that entry is lost, and the caller goes on. A nil l stays nil.
func Guard(l LevelLogger) LevelLogger {
	if l == nil {
		return nil
	}
	return guarded{l}
}

type guarded struct{ l LevelLogger }

func (g guarded) Debugf(format string, args ...any) {
	defer lose()
	g.l.Debugf(format, args...)
}

func (g guarded) Infof(format string, args ...any) {
	defer lose()
	g.l.Infof(format, args...)
}

func (g guarded) Warnf(format string, args ...any) {
	defer lose()
	g.l.Warnf(format, args...)
}

func (g guarded) Errorf(format string, args ...any) {
	defer lose()
	g.l.Errorf(format, args...)
}
