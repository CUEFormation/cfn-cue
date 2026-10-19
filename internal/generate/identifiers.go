package generate

import "strings"

// The selection below follows cfn-lint v1.57.2 at commit
// be66fb3e224b41c85065cb3002ad0b24e31db535,
// src/cfnlint/rules/resources/PrimaryIdentifiers.py (rule E3019). The
// exception list is Copyright Amazon.com, Inc. or its affiliates,
// SPDX-License-Identifier: MIT-0.

// identifierExceptions replaces a schema's primaryIdentifier. CodeBuild
// projects are identified by a read-only Id, but Name must still be unique.
var identifierExceptions = map[string][]string{
	"AWS::CodeBuild::Project": {"Name"},
}

// PrimaryIdentifier returns the property names that make up a resource's
// primary identifier when a template can set all of them. It returns nil when
// the schema has no identifier, when any part is read-only (CloudFormation
// assigns it), or when any part is nested below the top level of Properties.
func PrimaryIdentifier(root map[string]any) []string {
	typ, _ := root["typeName"].(string)
	if names, ok := identifierExceptions[typ]; ok {
		return append([]string(nil), names...)
	}
	readOnly := map[string]bool{}
	for _, v := range asArray(root["readOnlyProperties"]) {
		if p, ok := v.(string); ok {
			readOnly[p] = true
		}
	}
	var names []string
	for _, v := range asArray(root["primaryIdentifier"]) {
		p, _ := v.(string)
		name, ok := strings.CutPrefix(p, "/properties/")
		if !ok || name == "" || strings.Contains(name, "/") || readOnly[p] {
			return nil
		}
		names = append(names, name)
	}
	return names
}
