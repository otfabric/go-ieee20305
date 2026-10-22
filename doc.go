// SPDX-License-Identifier: MIT

// Package ieee20305 is the resource model of IEEE 2030.5 (Smart Energy
// Profile 2.0): every type of the IEEE 2030.5-2023 schema (sep.xsd, schema
// version 2.2), generated from the official schema with XML tags that
// preserve its namespace, element order, optionality and cardinality.
//
// The generated files (*_gen.go) are not edited by hand; see generate.go.
// They provide:
//
//   - a Go type for every schema type: structs that embed their base type,
//     pointers for optional elements and attributes, slices for repeated
//     elements, and named scalar types such as UInt32, String32 and the
//     hexadecimal HexBinary160;
//   - Validate methods that check the facets and cardinalities of the schema
//     and report a *ValidationError;
//   - Get methods that return the schema default of an absent value, such as
//     DeviceCapability.GetPollRate;
//   - the document elements: ElementName and NewElement;
//   - the resource table of the IEEE 2030.5 WADL: Resources and
//     LookupResource.
//
// Marshal, Unmarshal and Decode encode and decode documents. Elements a
// schema admits without declaring (xs:any) are kept as ExtensionElements.
//
// Entry points:
//
//   - package csipaus: the CSIP-AUS (Australia) profile.
//   - package client: an IEEE 2030.5 client for a utility server.
//   - package server: an IEEE 2030.5 server as an http.Handler.
//   - package security: mutual TLS, LFDI/SFDI derivation, certificate
//     chain validation.
//   - package schedule: primacy-based event scheduling and the
//     active/superseded/cancelled state machine.
//   - package discovery: DNS-SD service discovery for IEEE 2030.5 servers.
package ieee20305
