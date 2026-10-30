package generate

import "github.com/cueformation/cfn-cue/internal/bundle"

// Lifecycle copies the provider's lifecycle keywords from a resource schema
// for the manifest. Values of the wrong type are skipped, as in Attributes.
func Lifecycle(root map[string]any) bundle.Lifecycle {
	l := bundle.Lifecycle{
		PrimaryIdentifier:               stringsOf(root["primaryIdentifier"]),
		CreateOnlyProperties:            stringsOf(root["createOnlyProperties"]),
		ConditionalCreateOnlyProperties: stringsOf(root["conditionalCreateOnlyProperties"]),
		WriteOnlyProperties:             stringsOf(root["writeOnlyProperties"]),
		ReadOnlyProperties:              stringsOf(root["readOnlyProperties"]),
		DeprecatedProperties:            stringsOf(root["deprecatedProperties"]),
	}
	l.ReplacementStrategy, _ = root["replacementStrategy"].(string)
	for _, v := range asArray(root["additionalIdentifiers"]) {
		if ids := stringsOf(v); ids != nil {
			l.AdditionalIdentifiers = append(l.AdditionalIdentifiers, ids)
		}
	}
	if m, ok := root["propertyTransform"].(map[string]any); ok {
		for k, v := range m {
			if s, ok := v.(string); ok {
				if l.PropertyTransform == nil {
					l.PropertyTransform = map[string]string{}
				}
				l.PropertyTransform[k] = s
			}
		}
	}
	if m, ok := root["tagging"].(map[string]any); ok {
		flag := func(key string) *bool {
			if b, ok := m[key].(bool); ok {
				return &b
			}
			return nil
		}
		l.Tagging = &bundle.Tagging{Taggable: flag("taggable"), TagOnCreate: flag("tagOnCreate"), TagUpdatable: flag("tagUpdatable"), CloudFormationSystemTags: flag("cloudFormationSystemTags"), Permissions: stringsOf(m["permissions"])}
		l.Tagging.TagProperty, _ = m["tagProperty"].(string)
	}
	if m, ok := root["handlers"].(map[string]any); ok {
		for name, v := range m {
			h, ok := v.(map[string]any)
			if !ok {
				continue
			}
			handler := bundle.Handler{Permissions: stringsOf(h["permissions"])}
			if n, ok := h["timeoutInMinutes"].(float64); ok {
				handler.TimeoutInMinutes = int(n)
			}
			if l.Handlers == nil {
				l.Handlers = map[string]bundle.Handler{}
			}
			l.Handlers[name] = handler
		}
	}
	return l
}
