// SPDX-License-Identifier: MIT

package ieee20305

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
)

// Logger receives the log of the clients, servers and agents of this
// module: set it as the Logger of their configuration. A nil Logger logs
// nothing. Debug is the detail of every request; Info, what changes
// (a registration, a subscription); Warn, what failed and is retried;
// Error, what fails a request or stops work.
//
// A Logger is called from many goroutines, and must not block.
type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

// FieldLogger is a Logger that keeps fields (key-value pairs) with its
// lines: With returns one that adds kv to every line. The packages of this
// module name themselves (component=server, ...) through it when a Logger
// is one.
type FieldLogger interface {
	Logger
	With(kv ...any) FieldLogger
}

// NopLogger returns a Logger that logs nothing.
func NopLogger() Logger { return nopLogger{} }

type nopLogger struct{}

func (nopLogger) Debugf(string, ...any) {}
func (nopLogger) Infof(string, ...any)  {}
func (nopLogger) Warnf(string, ...any)  {}
func (nopLogger) Errorf(string, ...any) {}

// NewSlogLogger returns a FieldLogger that writes to the slog handler h,
// at the slog levels of the same names.
func NewSlogLogger(h slog.Handler) FieldLogger { return slogLogger{l: slog.New(h)} }

type slogLogger struct{ l *slog.Logger }

func (s slogLogger) log(level slog.Level, format string, args []any) {
	ctx := context.Background()
	if s.l.Enabled(ctx, level) {
		s.l.Log(ctx, level, fmt.Sprintf(format, args...))
	}
}

func (s slogLogger) Debugf(format string, args ...any) { s.log(slog.LevelDebug, format, args) }
func (s slogLogger) Infof(format string, args ...any)  { s.log(slog.LevelInfo, format, args) }
func (s slogLogger) Warnf(format string, args ...any)  { s.log(slog.LevelWarn, format, args) }
func (s slogLogger) Errorf(format string, args ...any) { s.log(slog.LevelError, format, args) }
func (s slogLogger) With(kv ...any) FieldLogger        { return slogLogger{l: s.l.With(kv...)} }

// NewStdLogger returns a Logger that writes to l (the standard logger when
// nil), with the level in front of each line, from Info up; NewStdLogger
// with debug true writes Debug lines too.
func NewStdLogger(l *log.Logger, debug bool) FieldLogger {
	if l == nil {
		l = log.New(os.Stderr, "", log.LstdFlags)
	}
	return stdLogger{l: l, debug: debug}
}

type stdLogger struct {
	l      *log.Logger
	debug  bool
	fields string
}

func (s stdLogger) out(level, format string, args []any) {
	_ = s.l.Output(3, level+" "+fmt.Sprintf(format, args...)+s.fields)
}

func (s stdLogger) Debugf(format string, args ...any) {
	if s.debug {
		s.out("DEBUG", format, args)
	}
}
func (s stdLogger) Infof(format string, args ...any)  { s.out("INFO", format, args) }
func (s stdLogger) Warnf(format string, args ...any)  { s.out("WARN", format, args) }
func (s stdLogger) Errorf(format string, args ...any) { s.out("ERROR", format, args) }

func (s stdLogger) With(kv ...any) FieldLogger {
	var b strings.Builder
	b.WriteString(s.fields)
	for i := 0; i < len(kv); i += 2 {
		if i+1 < len(kv) {
			fmt.Fprintf(&b, " %v=%v", kv[i], kv[i+1])
		} else {
			fmt.Fprintf(&b, " %v", kv[i])
		}
	}
	return stdLogger{l: s.l, debug: s.debug, fields: b.String()}
}

// ComponentLogger returns the Logger a package of this module uses for l:
// NopLogger for nil, and for a FieldLogger one with component=name.
func ComponentLogger(l Logger, name string) Logger {
	switch l := l.(type) {
	case nil:
		return nopLogger{}
	case FieldLogger:
		return l.With("component", name)
	default:
		return l
	}
}
