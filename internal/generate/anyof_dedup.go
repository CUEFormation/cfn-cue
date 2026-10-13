package generate

import "encoding/json"

// deduplicateAnyOf removes exact duplicate normalized JSON Schema branches.
// Normalization can erase unsupported annotations, turning distinct source
// branches into identical constraints. Keeping both can leave concrete CUE
// arrays or objects incomplete. This applies only to anyOf: repeated oneOf
// branches affect exclusivity and must remain distinct.
func deduplicateAnyOf(alternatives []any) []any {
	seen := make(map[string]bool, len(alternatives))
	unique := make([]any, 0, len(alternatives))
	for _, alternative := range alternatives {
		canonical, err := json.Marshal(alternative)
		if err != nil {
			// Schema input is JSON, but preserve an unrepresentable branch
			// if called with another value rather than treating it as equal.
			unique = append(unique, alternative)
			continue
		}
		key := string(canonical)
		if !seen[key] {
			seen[key] = true
			unique = append(unique, alternative)
		}
	}
	return unique
}
