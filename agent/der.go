// SPDX-License-Identifier: MIT

package agent

import (
	"bytes"
	"context"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// pollDefault reads the DefaultDERControl of the DERProgram of highest
// precedence and passes it on when it changed.
func (a *Agent) pollDefault(ctx context.Context) error {
	a.mu.Lock()
	p, _ := a.loops[DER].best.(*ieee20305.DERProgram)
	a.mu.Unlock()
	if a.cfg.OnDefaultDERControl == nil || p == nil || p.DefaultDERControlLink == nil {
		return nil
	}
	var d ieee20305.DefaultDERControl
	if err := a.c.Get(ctx, p.DefaultDERControlLink.Href, &d); err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}
	out, err := ieee20305.Marshal(&d)
	if err != nil {
		return err
	}
	a.mu.Lock()
	same := bytes.Equal(out, a.lastDefault)
	a.lastDefault = out
	a.mu.Unlock()
	if same {
		return nil
	}
	return a.cfg.OnDefaultDERControl(ctx, &d)
}
