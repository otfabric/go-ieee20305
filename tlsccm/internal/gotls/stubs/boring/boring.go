// Package boring stubs crypto/internal/boring for this copy of crypto/tls.
// BoringCrypto is disabled in this copy.
package boring

import "crypto/cipher"

// Enabled reports whether BoringCrypto is available. Always false in this copy.
const Enabled = false

// Unreachable is a no-op. In the real boring package, it panics if
// BoringCrypto is enabled and execution reaches non-boring code paths.
func Unreachable() {}

// NewGCMTLS is never called when Enabled=false. Stub satisfies the interface.
func NewGCMTLS(c cipher.Block) (cipher.AEAD, error) {
	panic("boring: not available")
}
