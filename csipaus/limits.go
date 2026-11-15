// SPDX-License-Identifier: MIT

package csipaus

import ieee20305 "github.com/otfabric/go-ieee20305"

// Limits are the site limits of CSIP-AUS, the opMod controls it adds to
// DERControlBase: a dynamic operating envelope. A nil limit is not set.
type Limits struct {
	// Export and Import limit the active power at the connection point.
	Export, Import *ieee20305.ActivePower
	// Generation and Load limit the active power of the site's generation
	// and load.
	Generation, Load *ieee20305.ActivePower
	// StorageTarget is the target of the site's storage.
	StorageTarget *ieee20305.ActivePower
}

// Apply sets the controls of b to the limits.
func (l Limits) Apply(b *DERControlBase) {
	b.OpModExpLimW, b.OpModImpLimW = l.Export, l.Import
	b.OpModGenLimW, b.OpModLoadLimW = l.Generation, l.Load
	b.OpModStorageTargetW = l.StorageTarget
}

// LimitsOf returns the site limits of a DERControl, DefaultDERControl or
// DERControlBase of CSIP-AUS, and whether v is one; a nil pointer is not.
func LimitsOf(v any) (Limits, bool) {
	var b *DERControlBase
	switch v := v.(type) {
	case *DERControl:
		if v == nil {
			return Limits{}, false
		}
		b = &v.DERControlBase
	case *DefaultDERControl:
		if v == nil {
			return Limits{}, false
		}
		b = &v.DERControlBase
	case *DERControlBase:
		if v == nil {
			return Limits{}, false
		}
		b = v
	default:
		return Limits{}, false
	}
	return Limits{Export: b.OpModExpLimW, Import: b.OpModImpLimW, Generation: b.OpModGenLimW,
		Load: b.OpModLoadLimW, StorageTarget: b.OpModStorageTargetW}, true
}
