// Package fipstls stubs crypto/internal/boring/fipstls for this copy of crypto/tls.
package fipstls

// Required returns false — FIPS TLS is not enforced in this copy.
func Required() bool {
	return false
}

// Force is a no-op in the stub.
func Force() {}

// Abandon is a no-op in the stub.
func Abandon() {}
