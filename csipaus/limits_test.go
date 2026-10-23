// SPDX-License-Identifier: MIT

package csipaus_test

import (
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/csipaus"
)

func watts(w int16) *ieee20305.ActivePower { return &ieee20305.ActivePower{Value: ieee20305.Int16(w)} }

func TestLimits(t *testing.T) {
	l := csipaus.Limits{Export: watts(1), Import: watts(2), Generation: watts(3), Load: watts(4), StorageTarget: watts(5)}
	var b csipaus.DERControlBase
	b.OpModConnect = ptr(true) // a control of the base standard is left alone
	l.Apply(&b)
	if b.OpModExpLimW.Value != 1 || b.OpModImpLimW.Value != 2 || b.OpModGenLimW.Value != 3 ||
		b.OpModLoadLimW.Value != 4 || b.OpModStorageTargetW.Value != 5 || !*b.OpModConnect {
		t.Errorf("Apply: %+v", b)
	}
	// Applying fewer limits clears the others: an envelope is the whole of them.
	csipaus.Limits{Export: watts(9)}.Apply(&b)
	if b.OpModExpLimW.Value != 9 || b.OpModImpLimW != nil || b.OpModStorageTargetW != nil {
		t.Errorf("Apply of one limit: %+v", b)
	}

	ctl := &csipaus.DERControl{}
	l.Apply(&ctl.DERControlBase)
	dflt := &csipaus.DefaultDERControl{}
	l.Apply(&dflt.DERControlBase)
	for _, v := range []any{ctl, dflt, &ctl.DERControlBase} {
		got, ok := csipaus.LimitsOf(v)
		if !ok || got.Export.Value != 1 || got.Load.Value != 4 || got.StorageTarget.Value != 5 {
			t.Errorf("LimitsOf(%T) = %+v, %v", v, got, ok)
		}
	}
	var none *csipaus.DERControlBase
	for _, v := range []any{none, &ieee20305.DERControl{}, "nothing", nil} {
		if _, ok := csipaus.LimitsOf(v); ok {
			t.Errorf("LimitsOf(%T) found limits", v)
		}
	}
}
