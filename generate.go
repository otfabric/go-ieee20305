// SPDX-License-Identifier: MIT

//go:generate go run ./internal/gen -schema schemas/sep.xsd -out . -pkg ieee20305
//go:generate go run ./internal/gen -base schemas/sep.xsd -schema schemas/csipaus.xsd -out csipaus -pkg csipaus
//go:generate go run ./internal/gen -wadl schemas/sep.wadl -out . -pkg ieee20305

package ieee20305
