package gotls

import "testing"

// TestCCM8RanksAboveGCMInBothPreferenceOrders is core-go #136 criterion 2:
// CCM-8 must rank ahead of the ECDHE-ECDSA AES-128-GCM suite in both TLS 1.2
// preference orders, so pickCipherSuite (handshake_server.go) reaches it
// first regardless of which order a given build selects. hasAESGCMHardwareSupport
// is always false in this copy (the cpu stub reports no hardware, LOW-6), so
// a live handshake only ever exercises cipherSuitesPreferenceOrderNoAES;
// this test asserts on both slices directly to cover
// cipherSuitesPreferenceOrder as well, since no handshake in this
// environment can reach it. RED at 406baef: cipher_suites_ccm.go appended
// CCM-8 to the end of both slices, after GCM.
func TestCCM8RanksAboveGCMInBothPreferenceOrders(t *testing.T) {
	assertRanksAboveGCM(t, "cipherSuitesPreferenceOrder", cipherSuitesPreferenceOrder)
	assertRanksAboveGCM(t, "cipherSuitesPreferenceOrderNoAES", cipherSuitesPreferenceOrderNoAES)
}

func assertRanksAboveGCM(t *testing.T, name string, order []uint16) {
	t.Helper()

	ccmIdx, gcmIdx := -1, -1
	for i, id := range order {
		switch id {
		case TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8:
			ccmIdx = i
		case TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256:
			gcmIdx = i
		}
	}
	if ccmIdx == -1 {
		t.Fatalf("%s: CCM_8 not present", name)
	}
	if gcmIdx == -1 {
		t.Fatalf("%s: GCM not present", name)
	}
	if ccmIdx >= gcmIdx {
		t.Errorf("%s: CCM_8 at index %d, GCM at index %d; want CCM_8 to rank first", name, ccmIdx, gcmIdx)
	}
}
