package generate

import (
	"crypto/sha256"
	"fmt"
)

// preservePatternReferences saves referenced value schemas before normalization
// renames RE2-compatible pattern keys or drops incompatible ones. A referenced
// value remains constrained even when matching object keys is approximated.
func preservePatternReferences(root map[string]any) {
	defs, _ := root["definitions"].(map[string]any)
	saved := map[string]string{}
	var pending []string
	rewrite := func(schema map[string]any) {
		ref, ok := schema["$ref"].(string)
		if !ok {
			return
		}
		target, ok := localSchemaReference(root, ref)
		if !ok || !target.throughPattern || target.retained {
			return
		}
		switch target.value.(type) {
		case map[string]any, bool:
		default:
			return
		}
		name, exists := saved[ref]
		if !exists {
			if defs == nil {
				defs = map[string]any{}
				root["definitions"] = defs
			}
			sum := sha256.Sum256([]byte(ref))
			base := fmt.Sprintf("_PatternRef%x", sum[:8])
			name = base
			for suffix := 1; ; suffix++ {
				if _, exists := defs[name]; !exists {
					break
				}
				name = fmt.Sprintf("%s_%d", base, suffix)
			}
			saved[ref] = name
			defs[name] = clone(target.value)
			pending = append(pending, name)
		}
		schema["$ref"] = "#/definitions/" + name
	}
	walkSchemaObjects(root, rewrite)
	// Saved targets may themselves reference another renamed/dropped pattern,
	// including the same target. Register aliases before visiting their bodies.
	for i := 0; i < len(pending); i++ {
		walkSchemaObjects(defs[pending[i]], rewrite)
	}
}
