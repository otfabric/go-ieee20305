// SPDX-License-Identifier: MIT

package ieee20305_test

import (
	"bytes"
	"log"
	"log/slog"
	"strings"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

func TestSlogLogger(t *testing.T) {
	var buf bytes.Buffer
	l := ieee20305.NewSlogLogger(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	c := ieee20305.ComponentLogger(l, "server")
	c.Debugf("hidden %d", 1)
	c.Infof("info %d", 2)
	c.Warnf("warn %d", 3)
	c.Errorf("error %d", 4)
	out := buf.String()
	for _, want := range []string{`level=INFO msg="info 2" component=server`, `level=WARN msg="warn 3"`, `level=ERROR msg="error 4"`} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "hidden") {
		t.Error("a Debug line below the level")
	}
}

func TestStdLogger(t *testing.T) {
	var buf bytes.Buffer
	l := ieee20305.NewStdLogger(log.New(&buf, "", 0), false)
	c := ieee20305.ComponentLogger(l, "agent").(ieee20305.FieldLogger).With("odd")
	c.Debugf("hidden")
	c.Infof("info")
	c.Warnf("warn")
	c.Errorf("error")
	want := "INFO info component=agent odd\nWARN warn component=agent odd\nERROR error component=agent odd\n"
	if buf.String() != want {
		t.Errorf("got\n%s", buf.String())
	}
	buf.Reset()
	ieee20305.NewStdLogger(log.New(&buf, "", 0), true).Debugf("shown")
	if buf.String() != "DEBUG shown\n" {
		t.Errorf("debug: %q", buf.String())
	}
	if ieee20305.NewStdLogger(nil, false) == nil {
		t.Error("a nil standard logger")
	}
}

// plain is a Logger that is no FieldLogger.
type plain struct{ lines []string }

func (p *plain) Debugf(f string, a ...any) { p.lines = append(p.lines, f) }
func (p *plain) Infof(f string, a ...any)  { p.lines = append(p.lines, f) }
func (p *plain) Warnf(f string, a ...any)  { p.lines = append(p.lines, f) }
func (p *plain) Errorf(f string, a ...any) { p.lines = append(p.lines, f) }

func TestComponentLogger(t *testing.T) {
	l := ieee20305.ComponentLogger(nil, "x")
	l.Debugf("a")
	l.Infof("b")
	l.Warnf("c")
	l.Errorf("d")
	n := ieee20305.NopLogger()
	n.Debugf("a")
	n.Infof("b")
	n.Warnf("c")
	n.Errorf("d")
	p := &plain{}
	if ieee20305.ComponentLogger(p, "x") != ieee20305.Logger(p) {
		t.Error("a Logger that is no FieldLogger is not used as it is")
	}
}
