// SPDX-License-Identifier: MIT

package interop

import (
	"regexp"
	"testing"
)

// knownBug is a defect of an upstream stack that a test of the suite runs
// into, as in the known-bugs.tsv of ieee20305-interop: the test is skipped
// with its evidence while it fails, and fails once it passes, so that a
// fixed bug does not stay listed.
type knownBug struct {
	adapter  string         // the image's adapter
	role     string         // its role: client (against our server) or server (against our client)
	test     *regexp.Regexp // matches version/fixture/operation
	evidence string
	summary  string
}

var knownBugs = []knownBug{
	{"epri", "client", regexp.MustCompile(`^2018/baseline/(report|respond)$`),
		"https://github.com/epri-dev/IEEE-2030.5-Client/blob/505ee47d9c3ed43ce63087198d09f77338da627d/se_schema.c#L2235",
		"client_test writes every document in the name space of the draft sep.xsd 2.0.4, http://ieee.org/2030.5; " +
			"the server takes urn:ieee:std:2030.5:ns only and refuses its MirrorUsagePoint and Responses with 400"},
	{"py20305", "client", regexp.MustCompile(`^2018/csip-aus/poll-pricing$`),
		"https://github.com/DERSecurity/py20305/tree/79844938eea3d05f5432cdc5f2d07a103e87912b/src/py20305/models/csipaus",
		"py20305 does not model the RateComponentLink that csipaus-core-v1.3.xsd requires in a CSIP-AUS " +
			"TimeTariffInterval, rejects the TimeTariffIntervalList and finds no interval"},
}

// known returns the known bug of the test id of adapter in role, if any.
func known(adapter, role, id string) *knownBug {
	for i, b := range knownBugs {
		if b.adapter == adapter && b.role == role && b.test.MatchString(id) {
			return &knownBugs[i]
		}
	}
	return nil
}

// expect reports the outcome of a test with a known bug: a failure skips
// the test, a pass fails it as stale. It returns whether the test may go
// on as one without a known bug.
func (b *knownBug) expect(t *testing.T, failed bool, detail string) bool {
	t.Helper()
	switch {
	case b == nil:
		return true
	case failed:
		t.Skipf("KNOWN_BUG %s (%s): %s", b.summary, b.evidence, detail)
	default:
		t.Errorf("a known bug did not reproduce, remove it from knownBugs: %s", b.summary)
	}
	return false
}
