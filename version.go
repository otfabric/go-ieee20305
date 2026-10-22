// SPDX-License-Identifier: MIT

package ieee20305

// Version identifies a revision of the IEEE 2030.5 standard.
type Version string

// Supported standard revisions.
const (
	// Version2018 is IEEE 2030.5-2018.
	Version2018 Version = "2018"
	// Version2023 is IEEE 2030.5-2023.
	Version2023 Version = "2023"
)

// Profile identifies an implementation profile that constrains or extends
// the base IEEE 2030.5 standard for a particular jurisdiction.
type Profile string

// Implementation profiles.
const (
	// ProfileCSIP is the Common Smart Inverter Profile, used in
	// California for Rule 21 and the CPUC Smart Inverter mandate.
	ProfileCSIP Profile = "CSIP"
	// ProfileCSIPAUS is the Common Smart Inverter Profile — Australia,
	// used for AS/NZS 4777.2 compliance and AEMO requirements.
	ProfileCSIPAUS Profile = "CSIP-AUS"
)
