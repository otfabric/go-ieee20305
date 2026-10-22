// SPDX-License-Identifier: MIT

//go:generate go run ./internal/gen -schema schemas/sep.xsd -out . -pkg ieee20305
//go:generate go run ./internal/gen -base schemas/sep.xsd -schema schemas/csipaus.xsd -out csipaus -pkg csipaus

package ieee20305
