// SPDX-License-Identifier: MIT

// Package csipaus is the CSIP-AUS (Common Smart Inverter Profile, Australia)
// v1.3 profile of IEEE 2030.5, generated from the official CSIP-AUS schemas.
//
// CSIP-AUS redefines some IEEE 2030.5 types to append elements in its own
// namespace (Namespace), such as the dynamic operating envelope limits of
// DERControlBase and the connection point of EndDevice. This package holds
// those redefined types, the types that contain them (such as DERControl and
// DERControlList), and the types of the CSIP-AUS namespace. Every other type
// is the one of the base package ieee20305.
//
// Marshal, Unmarshal and Decode encode and decode documents of both
// packages. CSIP-AUS v1.3 profiles IEEE 2030.5-2018, so Marshal does not
// write the schemaVer attribute of IEEE 2030.5-2023.
//
// The *_gen.go files are generated; do not edit them.
package csipaus
