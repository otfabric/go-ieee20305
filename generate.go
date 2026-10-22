// SPDX-License-Identifier: MIT

// Code generation directives. Running "go generate" or "make generate"
// regenerates all *_gen.go files from the IEEE 2030.5 XSD and WADL schemas.
// The generated files carry a "DO NOT EDIT" header with the source schema's
// SHA-256 checksum — do not edit them by hand; fix the generator instead.
// See schemas/README.md for how to obtain the schema files.

//go:generate go run ./internal/gen -schema schemas/sep.xsd -out . -pkg ieee20305
//go:generate go run ./internal/gen -base schemas/sep.xsd -schema schemas/csipaus.xsd -out csipaus -pkg csipaus
//go:generate go run ./internal/gen -wadl schemas/sep.wadl -out . -pkg ieee20305

package ieee20305
