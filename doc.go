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
//   - package client: an IEEE 2030.5 client of one server, over mutual TLS.
//   - package agent: the behaviour of a device on a client: registration,
//     polling, events and their Responses, metering, subscriptions.
//   - package server: an IEEE 2030.5 server as an http.Handler, with an
//     Admin API to provision devices and publish programs and events.
//   - package security: device identifiers (LFDI, SFDI), certificate
//     verification and TLS configurations.
//   - package schedule: event scheduling by primacy, supersession and
//     randomization.
//   - package discovery: DNS-SD discovery of servers.
//   - package csipaus, with csipaus/csipserver and csipaus/csipclient: the
//     CSIP-AUS (Australia) profile.
//   - module github.com/otfabric/go-ieee20305/tlsccm: the cipher suite
//     TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8, which crypto/tls lacks.
//
// Errors match the sentinels of this package (ErrNotFound, ErrTimeout, ...;
// see Retryable), and the client, server and agent take a Logger, a
// RetryPolicy and metrics hooks. The examples directory of the repository
// has runnable programs.
package ieee20305
