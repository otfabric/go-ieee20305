// SPDX-License-Identifier: MIT

package security_test

import (
	"fmt"

	"github.com/otfabric/go-ieee20305/security"
)

// The SFDI of a device follows from its LFDI, with a check digit.
func ExampleLFDI_SFDI() {
	lfdi, err := security.ParseLFDI("3E4F45AB31EDFE5B67E343E5E4562E31984E23E5")
	if err != nil {
		panic(err)
	}
	sfdi := lfdi.SFDI()
	fmt.Println(sfdi, sfdi.Valid())
	// Output:
	// 167261211391 true
}

// Derive the identifiers of a device from its certificate.
func ExampleCertificateLFDI() {
	ca, caKey, err := security.TestCA("example CA")
	if err != nil {
		panic(err)
	}
	cert, _, err := security.TestDeviceCert(ca, caKey, "inverter-1")
	if err != nil {
		panic(err)
	}
	lfdi := security.CertificateLFDI(cert)
	fmt.Println(len(lfdi.String()), lfdi.SFDI().Valid())
	// Output:
	// 40 true
}
