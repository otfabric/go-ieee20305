// SPDX-License-Identifier: MIT

package emit

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// GoName converts an XML name to an exported Go identifier. The first letter
// is upper-cased; underscores are dropped and the letter after them is
// upper-cased, except that an underscore before a digit is kept so that
// version suffixes stay readable: "DeviceCapability_r2_3" becomes
// "DeviceCapabilityR2_3" and "lFDI" becomes "LFDI".
func GoName(s string) string {
	var b strings.Builder
	upper := true
	for i, r := range s {
		switch {
		case r == '_' || r == '-' || r == '.':
			next, _ := utf8.DecodeRuneInString(s[i+1:])
			if unicode.IsDigit(next) && b.Len() > 0 {
				b.WriteByte('_')
			}
			upper = true
		case upper:
			b.WriteRune(unicode.ToUpper(r))
			upper = false
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// goBuiltin maps an XML Schema built-in to its Go type. hexBinary maps to
// []byte and gets text methods on the named type that derives from it.
var goBuiltin = map[string]string{
	"string":        "string",
	"anyURI":        "string",
	"boolean":       "bool",
	"hexBinary":     "[]byte",
	"byte":          "int8",
	"short":         "int16",
	"int":           "int32",
	"long":          "int64",
	"unsignedByte":  "uint8",
	"unsignedShort": "uint16",
	"unsignedInt":   "uint32",
	"unsignedLong":  "uint64",
}
