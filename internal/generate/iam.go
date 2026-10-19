package generate

import (
	_ "embed"
	"maps"
	"strconv"
	"strings"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/parser"
)

//go:embed cue/iam.cue
var iamSource []byte

const sourceModule = "github.com/cueformation/cfn-cue"

// IAM returns the hand-written iam package with imports bound to module.
func IAM(module string) (*ast.File, error) {
	f, e := parser.ParseFile("iam/iam.cue", iamSource, parser.ParseComments)
	if e != nil {
		return nil, e
	}
	for spec := range f.ImportSpecs() {
		path, e := strconv.Unquote(spec.Path.Value)
		if e != nil {
			return nil, e
		}
		if strings.HasPrefix(path, sourceModule+"/") {
			spec.Path = ast.NewString(module + strings.TrimPrefix(path, sourceModule))
		}
	}
	return f, nil
}

// iamPolicies places iam profiles on provider schema paths. Paths are JSON
// pointers into the upstream resource schema. Property names alone are not
// enough: many "Policy" properties use other grammars.
var iamPolicies = map[string]map[string]string{
	"AWS::IAM::Role": {
		"#/properties/AssumeRolePolicyDocument":          "TrustPolicyValue",
		"#/definitions/Policy/properties/PolicyDocument": "IdentityPolicyValue",
	},
	"AWS::IAM::User":          {"#/definitions/Policy/properties/PolicyDocument": "IdentityPolicyValue"},
	"AWS::IAM::Group":         {"#/definitions/Policy/properties/PolicyDocument": "IdentityPolicyValue"},
	"AWS::IAM::Policy":        {"#/properties/PolicyDocument": "IdentityPolicyValue"},
	"AWS::IAM::ManagedPolicy": {"#/properties/PolicyDocument": "IdentityPolicyValue"},
	"AWS::IAM::RolePolicy":    {"#/properties/PolicyDocument": "IdentityPolicyValue"},
	"AWS::IAM::UserPolicy":    {"#/properties/PolicyDocument": "IdentityPolicyValue"},
	"AWS::IAM::GroupPolicy":   {"#/properties/PolicyDocument": "IdentityPolicyValue"},
	"AWS::SSO::PermissionSet": {"#/properties/InlinePolicy": "IdentityPolicyValue"},
	"AWS::S3::BucketPolicy":   {"#/properties/PolicyDocument": "S3BucketPolicyValue"},
	"AWS::KMS::Key":           {"#/properties/KeyPolicy": "KMSKeyPolicyValue"},
	"AWS::KMS::ReplicaKey":    {"#/properties/KeyPolicy": "KMSKeyPolicyValue"},
}

// IAMPolicies returns the iam profile placed on each policy property, by
// resource type and schema pointer.
func IAMPolicies() map[string]map[string]string {
	out := make(map[string]map[string]string, len(iamPolicies))
	for typ, paths := range iamPolicies {
		out[typ] = maps.Clone(paths)
	}
	return out
}

// selectedPolicy is a policy property whose grammar depends on a sibling
// enum. Unlisted selector values, and intrinsic selectors, keep the
// provider schema.
type selectedPolicy struct {
	property, selector string
	profiles           map[string]string
}

var iamSelectedPolicies = map[string]selectedPolicy{
	"AWS::Organizations::Policy": {"Content", "Type", map[string]string{
		"SERVICE_CONTROL_POLICY":  "ServiceControlPolicyValue",
		"RESOURCE_CONTROL_POLICY": "ResourceControlPolicyValue",
	}},
}

func iamRef(profile string) map[string]any { return map[string]any{"$ref": "cue://iam/" + profile} }

// selectPolicy rewrites a normalized root so a selected property is checked
// by an if/then/else chain on its selector. The normalized provider schema
// is the final else branch, so a concrete selector meets one alternative
// rather than an intersection of the profile and the provider schema.
func (c *converter) selectPolicy(root map[string]any) {
	sel, ok := iamSelectedPolicies[c.resource]
	if !ok {
		return
	}
	path := "#/properties/" + pointer(sel.property)
	props, _ := root["properties"].(map[string]any)
	original, ok := props[sel.property]
	if !ok {
		c.warn(path, "iamPolicy", "IAM policy placement not found; property keeps its provider schema")
		return
	}
	var chain any = map[string]any{"properties": map[string]any{sel.property: original}}
	for _, value := range sortedStringKeys(sel.profiles) {
		chain = map[string]any{
			"if":   map[string]any{"required": []any{sel.selector}, "properties": map[string]any{sel.selector: map[string]any{"const": value}}},
			"then": map[string]any{"properties": map[string]any{sel.property: iamRef(sel.profiles[value])}},
			"else": chain,
		}
	}
	props[sel.property] = map[string]any{}
	root["allOf"] = append(asArray(root["allOf"]), chain)
}
