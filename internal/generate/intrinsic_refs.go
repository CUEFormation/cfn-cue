package generate

import (
	"strconv"
	"strings"
)

// localReferenceIsLifted reports whether a local reference points to a schema
// position normalize already lifts for template inputs. Lifting both that
// definition and its reference produces duplicate intrinsic alternatives;
// object-valued Fn::If expressions then remain incomplete in CUE v0.18.
//
// Applicator children (allOf, anyOf, oneOf, not, if/then/else) are deliberately
// excluded: normalize keeps those constraints unlifted so intrinsics cannot
// independently bypass parts of a constrained value. Walk only schema
// positions, never schema-shaped maps within const/enum/annotation data.
func localReferenceIsLifted(s, root map[string]any) bool {
	ref, ok := s["$ref"].(string)
	if !ok || !strings.HasPrefix(ref, "#/") {
		return false
	}
	parts := strings.Split(ref[2:], "/")
	for i, part := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
	}
	var current any = root
	lifted := false
	for len(parts) > 0 {
		node, ok := current.(map[string]any)
		if !ok {
			return false
		}
		keyword := parts[0]
		parts = parts[1:]
		child, exists := node[keyword]
		if !exists {
			return false
		}
		switch keyword {
		case "definitions", "$defs", "properties", "patternProperties", "dependentSchemas", "dependencies":
			// These keywords contain maps of schemas, not schemas themselves.
			children, ok := child.(map[string]any)
			if !ok || len(parts) == 0 {
				return false
			}
			current, exists = children[parts[0]]
			parts = parts[1:]
			if !exists {
				return false
			}
			lifted = keyword != "dependentSchemas" && keyword != "dependencies"
		case "items", "prefixItems", "allOf", "anyOf", "oneOf":
			if children, ok := child.([]any); ok {
				if len(parts) == 0 {
					return false
				}
				index, err := strconv.Atoi(parts[0])
				if err != nil || index < 0 || index >= len(children) || strconv.Itoa(index) != parts[0] {
					return false
				}
				current = children[index]
				parts = parts[1:]
				lifted = keyword == "items" || keyword == "prefixItems"
			} else {
				if keyword != "items" {
					return false
				}
				current, lifted = child, true
			}
		case "additionalItems":
			// normalize consumes this legacy keyword only beside tuple items.
			if !isArray(node["items"]) {
				return false
			}
			current, lifted = child, true
		case "additionalProperties", "contains":
			current, lifted = child, true
		case "not", "if", "then", "else", "propertyNames":
			current, lifted = child, false
		default:
			return false
		}
	}
	if !lifted {
		return false
	}
	switch schema := current.(type) {
	case map[string]any:
		return true
	case bool:
		return schema // a false schema is never lifted by normalize
	default:
		return false
	}
}
