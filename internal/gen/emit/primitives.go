// SPDX-License-Identifier: MIT

package emit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/otfabric/go-ieee20305/internal/gen/xsd"
)

func (e *Emitter) emitPrimitives() error {
	var b strings.Builder
	b.WriteString(e.header())
	fmt.Fprintf(&b, "package %s\n", e.pkgName)

	// Separate enums and type aliases.
	var enums []*xsd.SimpleType
	var aliases []*xsd.SimpleType
	for _, st := range e.r.SimpleTypes {
		if st.Name == "" {
			continue
		}
		if st.IsEnum() {
			enums = append(enums, st)
		} else {
			aliases = append(aliases, st)
		}
	}

	sort.Slice(aliases, func(i, j int) bool { return aliases[i].Name < aliases[j].Name })
	sort.Slice(enums, func(i, j int) bool { return enums[i].Name < enums[j].Name })

	// Type aliases.
	if len(aliases) > 0 {
		b.WriteString("\n// --- Primitive types ---\n\n")
		for _, st := range aliases {
			typeName := GoTypeName(st.Name)
			goBase := resolveSimpleBase(st)
			if st.Doc != "" {
				fmt.Fprintf(&b, "// %s is generated from xs:simpleType %q.\n//\n// %s\n", typeName, st.Name, st.Doc)
			} else {
				fmt.Fprintf(&b, "// %s is generated from xs:simpleType %q.\n", typeName, st.Name)
			}
			fmt.Fprintf(&b, "type %s %s\n\n", typeName, goBase)
		}
	}

	// Enums.
	if len(enums) > 0 {
		b.WriteString("\n// --- Enumeration types ---\n\n")
		for _, st := range enums {
			typeName := GoTypeName(st.Name)
			fmt.Fprintf(&b, "// %s is generated from xs:simpleType %q.\n", typeName, st.Name)
			fmt.Fprintf(&b, "type %s string\n\n", typeName)
			fmt.Fprintf(&b, "const (\n")
			usedConsts := make(map[string]int)
			for _, val := range st.Enumerations {
				constName := goEnumConstName(typeName, val)
				if n, dup := usedConsts[constName]; dup {
					usedConsts[constName] = n + 1
					constName = fmt.Sprintf("%s_%d", constName, n+1)
				} else {
					usedConsts[constName] = 1
				}
				fmt.Fprintf(&b, "\t%s %s = %q\n", constName, typeName, val)
			}
			fmt.Fprintf(&b, ")\n\n")
		}
	}

	return e.writeFile("primitives_gen.go", b.String())
}

func resolveSimpleBase(st *xsd.SimpleType) string {
	if st.Base == "" {
		return "string"
	}
	// xs:hexBinary with a fixed length → [N]byte.
	if st.Base == "xs:hexBinary" && st.MinLength > 0 && st.MinLength == st.MaxLength {
		return fmt.Sprintf("[%d]byte", st.MinLength)
	}
	if xsd.IsBuiltinXSD(st.Base) {
		return XSDToGoType(st.Base)
	}
	return "string"
}
