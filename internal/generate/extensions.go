package generate

import (
	"embed"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/token"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// cfn-lint v1.57.2, at commit be66fb3e224b41c85065cb3002ad0b24e31db535, adds
// service rules to the provider schemas as JSON Schema "extensions" in
// src/cfnlint/data/schemas/extensions. The files below are copied unchanged.
// They are Copyright Amazon.com, Inc. or its affiliates,
// SPDX-License-Identifier: MIT-0.
//
// Only error-level rules that check a single resource are used. Rules that
// gather other resources (cfnGather), list instance types per region, or carry
// the multi-megabyte RDS instance class tables are left to cfn-lint.
//
//go:embed cfnlint/extensions
var extensionFiles embed.FS

// readExtension reads a vendored file; tests replace it.
var readExtension = func(name string) ([]byte, error) {
	return extensionFiles.ReadFile("cfnlint/extensions/" + name)
}

// extension applies one cfn-lint schema file at Paths, which follow
// cfn-lint's keywords below Resources/<type>/Properties: "" is Properties
// itself, a name selects a property, and * selects every list item.
type extension struct {
	Rule  string
	File  string
	Paths map[string][]string
	// cfn-lint lowercases a string Engine before these rules compare it.
	LowercaseEngine bool
	// Scoped rules are compiled by scopedExtensions to comprehensions that
	// read only the fields they name. As matchIf validators, each of which
	// re-checks the whole resource, these added 25-340 ms per large
	// resource in the corpus survey, against a 40-90 ms baseline.
	Scoped bool
	// Message reports a failed scoped rule after its ID.
	Message string
}

var cfnLintExtensions = []extension{
	{Rule: "E3718", File: "aws_apigateway_authorizer/ttl.json", Paths: map[string][]string{"AWS::ApiGateway::Authorizer": {""}}},
	{Rule: "E3660", File: "aws_apigateway_restapi/openapi_properties.json", Paths: map[string][]string{"AWS::ApiGateway::RestApi": {""}}},
	{Rule: "E3723", File: "aws_apigateway_stage/method_settings_resource_path.json", Paths: map[string][]string{"AWS::ApiGateway::Stage": {"MethodSettings/*"}}},
	{Rule: "E3722", File: "aws_cloudfront_distribution/forward_enum.json", Paths: map[string][]string{"AWS::CloudFront::Distribution": {"DistributionConfig/DefaultCacheBehavior", "DistributionConfig/CacheBehaviors/*"}}},
	{Rule: "E3615", File: "aws_cloudwatch_alarm/period.json", Paths: map[string][]string{"AWS::CloudWatch::Alarm": {""}}},
	{Rule: "E3636", File: "aws_codebuild_project/s3_locations.json", Paths: map[string][]string{"AWS::CodeBuild::Project": {"Artifacts", "Source"}}},
	{Rule: "E3638", File: "aws_dynamodb_table/billingmode_exclusive.json", Paths: map[string][]string{"AWS::DynamoDB::Table": {""}}, Scoped: true, Message: "when BillingMode is PAY_PER_REQUEST, ProvisionedThroughput capacity units must be 0"},
	{Rule: "E3639", File: "aws_dynamodb_table/billingmode_provisioned_dependent.json", Paths: map[string][]string{"AWS::DynamoDB::Table": {""}}, Scoped: true, Message: "when BillingMode is PROVISIONED or omitted, ProvisionedThroughput with capacity units of at least 1 is required"},
	{Rule: "E3671", File: "aws_ec2_instance/blockdevicemappings.json", Paths: map[string][]string{
		"AWS::AutoScaling::LaunchConfiguration": {"BlockDeviceMappings/*/Ebs"},
		"AWS::EC2::Instance":                    {"BlockDeviceMappings/*/Ebs"},
		"AWS::EC2::LaunchTemplate":              {"LaunchTemplateData/BlockDeviceMappings/*/Ebs"},
		"AWS::EC2::SpotFleet":                   {"SpotFleetRequestConfigData/LaunchSpecifications/*/BlockDeviceMappings/*/Ebs"},
		"AWS::OpsWorks::Instance":               {"BlockDeviceMappings/*/Ebs"},
	}},
	{Rule: "E3674", File: "aws_ec2_instance/privateipaddress.json", Paths: map[string][]string{"AWS::EC2::Instance": {""}, "AWS::EC2::NetworkInterface": {""}}},
	{Rule: "E3715", File: "aws_ec2_instance/virtualname_ephemeral.json", Paths: map[string][]string{
		"AWS::AutoScaling::LaunchConfiguration": {"BlockDeviceMappings/*"},
		"AWS::EC2::Instance":                    {"BlockDeviceMappings/*"},
		"AWS::EC2::LaunchTemplate":              {"LaunchTemplateData/BlockDeviceMappings/*"},
		"AWS::EC2::SpotFleet":                   {"SpotFleetRequestConfigData/LaunchSpecifications/*/BlockDeviceMappings/*"},
		"AWS::OpsWorks::Instance":               {"BlockDeviceMappings/*"},
	}},
	{Rule: "E3688", File: "aws_ec2_securitygroup/all_to_and_from_ports.json", Paths: securityGroupRulePaths},
	{Rule: "E3687", File: "aws_ec2_securitygroup/protocols_and_port_ranges_include.json", Paths: securityGroupRulePaths},
	{Rule: "E3044", File: "aws_ecs_service/fargate.json", Paths: map[string][]string{"AWS::ECS::Service": {""}}},
	{Rule: "E3042", File: "aws_ecs_taskdefinition/containerdefinitions_essential.json", Paths: map[string][]string{"AWS::ECS::TaskDefinition": {"ContainerDefinitions"}}, Scoped: true, Message: "at least one container must be essential"},
	{Rule: "E3047", File: "aws_ecs_taskdefinition/fargate_cpu_memory.json", Paths: map[string][]string{"AWS::ECS::TaskDefinition": {""}}, Scoped: true, Message: "Fargate task Cpu and Memory must be a supported combination"},
	{Rule: "E3048", File: "aws_ecs_taskdefinition/fargate_properties.json", Paths: map[string][]string{"AWS::ECS::TaskDefinition": {""}}, Scoped: true, Message: "Fargate tasks require NetworkMode awsvpc, Cpu and Memory, a supported Cpu and log driver, and no PlacementConstraints"},
	{Rule: "E3046", File: "aws_ecs_taskdefinition/logging_configuration.json", Paths: map[string][]string{"AWS::ECS::TaskDefinition": {"ContainerDefinitions/*/LogConfiguration"}}},
	{Rule: "E3695", File: "aws_elasticache_cachecluster/engine_version.json", Paths: map[string][]string{"AWS::ElastiCache::CacheCluster": {""}}},
	{Rule: "E3704", File: "aws_elasticache_replicationgroup/transitencryptionenabled_valkey.json", Paths: map[string][]string{"AWS::ElastiCache::ReplicationGroup": {""}}},
	{Rule: "E3679", File: "aws_elasticloadbalancing_loadbalancer/certificate.json", Paths: map[string][]string{"AWS::ElasticLoadBalancing::LoadBalancer": {"Listeners/*"}}},
	{Rule: "E3676", File: "aws_elasticloadbalancingv2_listener/certificate.json", Paths: map[string][]string{"AWS::ElasticLoadBalancingV2::Listener": {""}}},
	{Rule: "E3680", File: "aws_elasticloadbalancingv2_loadbalancer/application_subnets.json", Paths: map[string][]string{"AWS::ElasticLoadBalancingV2::LoadBalancer": {""}}},
	{Rule: "E3684", File: "aws_elasticloadbalancingv2_targetgroup/healthcheckprotocol_restrictions.json", Paths: map[string][]string{"AWS::ElasticLoadBalancingV2::TargetGroup": {""}}},
	{Rule: "E3683", File: "aws_elasticloadbalancingv2_targetgroup/protocol_restrictions.json", Paths: map[string][]string{"AWS::ElasticLoadBalancingV2::TargetGroup": {""}}},
	{Rule: "E3681", File: "aws_elasticloadbalancingv2_targetgroup/targettype_restrictions.json", Paths: map[string][]string{"AWS::ElasticLoadBalancingV2::TargetGroup": {""}}},
	{Rule: "E3725", File: "aws_fsx_filesystem/storage_capacity.json", Paths: map[string][]string{"AWS::FSx::FileSystem": {""}}},
	{Rule: "E3634", File: "aws_lambda_eventsourcemapping/eventsourcearn_sqs_exclusive.json", Paths: map[string][]string{"AWS::Lambda::EventSourceMapping": {""}}},
	{Rule: "E3633", File: "aws_lambda_eventsourcemapping/eventsourcearn_stream_inclusive.json", Paths: map[string][]string{"AWS::Lambda::EventSourceMapping": {""}}},
	{Rule: "E3717", File: "aws_lambda_function/capacity_provider_timeout.json", Paths: map[string][]string{"AWS::Lambda::Function": {""}}},
	{Rule: "E3696", File: "aws_lambda_function/loglevel_logformat.json", Paths: map[string][]string{"AWS::Lambda::Function": {""}}},
	{Rule: "E3685", File: "aws_lambda_function/packagetype_image_exclusions.json", Paths: map[string][]string{"AWS::Lambda::Function": {""}}},
	{Rule: "E3677", File: "aws_lambda_function/zipfile_runtime_enum.json", Paths: map[string][]string{"AWS::Lambda::Function": {""}}},
	{Rule: "E3678", File: "aws_lambda_function/zipfile_runtime_exists.json", Paths: map[string][]string{"AWS::Lambda::Function": {""}}},
	{Rule: "E3693", File: "aws_rds_dbcluster/aurora.json", Paths: map[string][]string{"AWS::RDS::DBCluster": {""}}},
	{Rule: "E3690", File: "aws_rds_dbcluster/engine_version.json", Paths: map[string][]string{"AWS::RDS::DBCluster": {""}}, Scoped: true, Message: "Engine must be supported, and EngineVersion valid for the Engine"},
	{Rule: "E3689", File: "aws_rds_dbcluster/monitoring.json", Paths: map[string][]string{"AWS::RDS::DBCluster": {""}}},
	{Rule: "E3692", File: "aws_rds_dbcluster/multiaz.json", Paths: map[string][]string{"AWS::RDS::DBCluster": {""}}},
	{Rule: "E3686", File: "aws_rds_dbcluster/serverless_exclusive.json", Paths: map[string][]string{"AWS::RDS::DBCluster": {""}}},
	{Rule: "E3682", File: "aws_rds_dbinstance/aurora_exclusive.json", Paths: map[string][]string{"AWS::RDS::DBInstance": {""}}, LowercaseEngine: true},
	{Rule: "E3719", File: "aws_rds_dbinstance/backupretentionperiod_maximum.json", Paths: map[string][]string{"AWS::RDS::DBInstance": {""}}},
	{Rule: "E3691", File: "aws_rds_dbinstance/engine_version.json", Paths: map[string][]string{"AWS::RDS::DBInstance": {""}}, LowercaseEngine: true, Scoped: true, Message: "Engine must be supported, and EngineVersion valid for the Engine"},
	{Rule: "E3720", File: "aws_rds_dbinstance/kmskey_storageencrypted.json", Paths: map[string][]string{"AWS::RDS::DBInstance": {""}}},
	{Rule: "E3721", File: "aws_rds_dbinstance/replicamode_enum.json", Paths: map[string][]string{"AWS::RDS::DBInstance": {""}}},
	{Rule: "E3661", File: "aws_route53_healthcheck/healthcheckconfig_type_inclusive.json", Paths: map[string][]string{"AWS::Route53::HealthCheck": {"HealthCheckConfig"}}},
	{Rule: "E3029", File: "aws_route53_recordset/recordset_alias.json", Paths: recordSetPaths},
	{Rule: "E3023", File: "aws_route53_recordset/recordset_pattern.json", Paths: recordSetPaths},
	{Rule: "E3045", File: "aws_s3_bucket/ownershipcontrols.json", Paths: map[string][]string{"AWS::S3::Bucket": {""}}},
	{Rule: "E3501", File: "aws_sqs_queue/properties.json", Paths: map[string][]string{"AWS::SQS::Queue": {""}}},
}

var securityGroupRulePaths = map[string][]string{
	"AWS::EC2::SecurityGroup":        {"SecurityGroupIngress/*", "SecurityGroupEgress/*"},
	"AWS::EC2::SecurityGroupEgress":  {""},
	"AWS::EC2::SecurityGroupIngress": {""},
}

var recordSetPaths = map[string][]string{
	"AWS::Route53::RecordSet":      {""},
	"AWS::Route53::RecordSetGroup": {"RecordSets/*"},
}

// literalMarker marks extension schemas that must not admit intrinsics:
// conditions under if and not. cfn-lint never lets an unresolved intrinsic
// satisfy a condition, so a Ref does not switch a rule on.
const literalMarker = "x-cfn-cue-literal"

// applyExtensions adds the cfn-lint rules for the converter's resource to
// root, the resource's properties schema, before normalization.
func (c *converter) applyExtensions(root map[string]any) error {
	for _, ext := range cfnLintExtensions {
		paths, ok := ext.Paths[c.resource]
		if !ok || ext.Scoped {
			continue
		}
		b, err := readExtension(ext.File)
		if err != nil {
			return err
		}
		var schema any
		if err := json.Unmarshal(b, &schema); err != nil {
			return fmt.Errorf("%s: %w", ext.File, err)
		}
		schema = prepareExtension(schema, false, ext.LowercaseEngine, "")
		if pattern := unsupportedPattern(schema); pattern != "" {
			// A dropped pattern would widen a condition and apply the rule
			// to values cfn-lint exempts, so the whole rule is skipped.
			// Provider lookaround translation (lookaround.go) is not used
			// here; no vendored rule needs it.
			c.warn("#", "cfnLintExtension", ext.Rule+" not applied: pattern is incompatible with RE2: "+pattern)
			continue
		}
		moveDefinitions(root, schema.(map[string]any), "CfnLint"+ext.Rule)
		for _, path := range paths {
			target, err := extensionTarget(root, path)
			if err != nil {
				c.warn("#/"+path, "cfnLintExtension", ext.Rule+" not applied: "+err.Error())
				continue
			}
			target["allOf"] = append(asArray(target["allOf"]), schema)
		}
	}
	return nil
}

// moveDefinitions moves an extension's local definitions into root under
// prefixed names, so its references resolve after it is merged into root.
func moveDefinitions(root, schema map[string]any, prefix string) {
	definitions, ok := schema["definitions"].(map[string]any)
	if !ok {
		return
	}
	delete(schema, "definitions")
	rename := func(r string) string {
		name, ok := strings.CutPrefix(r, "#/definitions/")
		if !ok {
			return r
		}
		return "#/definitions/" + prefix + name
	}
	var rewrite func(any)
	rewrite = func(v any) {
		switch n := v.(type) {
		case map[string]any:
			if r, ok := n["$ref"].(string); ok {
				n["$ref"] = rename(r)
			}
			for _, x := range n {
				rewrite(x)
			}
		case []any:
			for _, x := range n {
				rewrite(x)
			}
		}
	}
	rewrite(schema)
	target, _ := root["definitions"].(map[string]any)
	if target == nil {
		target = map[string]any{}
		root["definitions"] = target
	}
	for name, definition := range definitions {
		rewrite(definition)
		target[prefix+name] = definition
	}
}

// extensionTarget finds the schema at path, following local references.
// A rule placed in a referenced definition applies wherever that definition
// is used; cfn-lint applies it only at the listed path.
func extensionTarget(root map[string]any, path string) (map[string]any, error) {
	node := root
	if path == "" {
		return node, nil
	}
	for _, step := range strings.Split(path, "/") {
		resolved, err := resolveLocal(root, node)
		if err != nil {
			return nil, err
		}
		var next any
		if step == "*" {
			next = resolved["items"]
		} else {
			properties, _ := resolved["properties"].(map[string]any)
			next = properties[step]
		}
		child, ok := next.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("path %q has no schema at %q", path, step)
		}
		node = child
	}
	// Attach to a referenced definition rather than beside the reference:
	// the converter gives a lone local reference no second intrinsic
	// alternative, because a duplicate leaves Fn::If values incomplete.
	return resolveLocal(root, node)
}

func resolveLocal(root, node map[string]any) (map[string]any, error) {
	for seen := 0; seen < 32; seen++ {
		r, ok := node["$ref"].(string)
		if !ok || !strings.HasPrefix(r, "#/") {
			return node, nil
		}
		var v any = root
		for _, p := range strings.Split(r[2:], "/") {
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("unresolved reference %s", r)
			}
			v = m[strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")]
		}
		next, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unresolved reference %s", r)
		}
		node = next
	}
	return nil, fmt.Errorf("reference cycle")
}

// prepareExtension rewrites cfn-lint's comparison keywords into standard
// JSON Schema with the same results, and marks condition subtrees.
//
// cfn-lint compares const and enum values loosely: "0" equals 0, and "true"
// equals true. Each literal becomes the set of JSON values cfn-lint treats
// as equal to it. With LowercaseEngine, cfn-lint lowercases a string Engine
// before comparing, so Engine comparisons and patterns become
// case-insensitive.
func prepareExtension(v any, literal, lowercaseEngine bool, property string) any {
	switch n := v.(type) {
	case []any:
		out := make([]any, len(n))
		for i, x := range n {
			out[i] = prepareExtension(x, literal, lowercaseEngine, "")
		}
		return out
	case map[string]any:
		out := map[string]any{}
		// Recognize these on the original not, before its anyOf is rewritten.
		absent, absentOK := absentNames(n["not"])
		presentName, presentSchema, presentOK := notPresentMatching(n["not"])
		for k, x := range n {
			switch k {
			case "additionalProperties":
				// true is the default; false is kept.
				if x != true {
					out[k] = prepareExtension(x, literal, lowercaseEngine, "")
				}
			case "if", "not":
				if k == "not" && (absentOK || presentOK) {
					continue
				}
				out[k] = prepareExtension(x, true, lowercaseEngine, "")
			case "properties", "patternProperties", "dependentSchemas":
				props := map[string]any{}
				for name, schema := range x.(map[string]any) {
					if schema == true {
						// true accepts anything. Normalization would
						// otherwise replace it with fn.#Value.
						props[name] = map[string]any{literalMarker: true}
						continue
					}
					props[name] = prepareExtension(schema, literal, lowercaseEngine, name)
				}
				out[k] = props
			case "pattern":
				// cfn-lint writes "does not start with X" as ^(?!X).*$, a
				// lookahead RE2 lacks. As a format-like keyword it ignores
				// non-strings, so the rewrite keeps that with type: string.
				if m := negativePrefix.FindStringSubmatch(fmt.Sprint(x)); m != nil {
					out["not"] = map[string]any{"type": "string", "pattern": "^" + m[1], literalMarker: true}
					continue
				}
				if lowercaseEngine && property == "Engine" {
					out[k] = "(?i)" + fmt.Sprint(x)
					continue
				}
				out[k] = x
			case "anyOf":
				items := prepareExtension(x, literal, lowercaseEngine, "").([]any)
				if !literal {
					out[k] = items
					continue
				}
				// The converter turns anyOf into a CUE disjunction, where a
				// branch missing a required property still unifies, so the
				// condition would always hold. matchN, which not and allOf
				// keep, checks required properties: anyOf(A, B) is
				// not(allOf(not A, not B)).
				var negated []any
				for _, item := range items {
					negated = append(negated, map[string]any{"not": item, literalMarker: true})
				}
				out["allOf"] = append(asArray(out["allOf"]), map[string]any{"not": map[string]any{"allOf": negated, literalMarker: true}, literalMarker: true})
			case "const", "enum":
				var values []any
				if k == "const" {
					values = []any{x}
				} else {
					values = asArray(x)
				}
				if lowercaseEngine && property == "Engine" && allStrings(values) {
					var quoted []string
					for _, value := range values {
						quoted = append(quoted, regexp.QuoteMeta(strings.ToLower(value.(string))))
					}
					sort.Strings(quoted)
					out["type"] = "string"
					out["pattern"] = "(?i)^(?:" + strings.Join(quoted, "|") + ")$"
					continue
				}
				out["enum"] = looseValues(values)
			default:
				out[k] = prepareExtension(x, literal, lowercaseEngine, "")
			}
		}
		if _, typed := out["type"]; literal && !typed && hasNumericKeyword(out) {
			// A numeric keyword ignores strings, so an untyped condition
			// such as maximum: 60 would hold for "300" and apply the rule.
			// cfn-lint compares such strings as numbers; here a condition
			// on numbers is not met by a string, which is more lenient.
			out["type"] = "number"
			// Typed directly, the condition would let the extractor drop
			// the string spellings in then as unreachable. A double not
			// hides the type from that inference without changing the
			// result.
			numeric := map[string]any{literalMarker: true}
			for _, k := range append([]string{"type"}, numericKeywords...) {
				if x, ok := out[k]; ok {
					numeric[k] = x
					delete(out, k)
				}
			}
			negated := map[string]any{"not": map[string]any{"not": numeric, literalMarker: true}, literalMarker: true}
			out["allOf"] = append(asArray(out["allOf"]), negated)
		}
		if absentOK {
			// CUE checks matchN(0, [{X!: _}]) against an empty struct when
			// it meets the schema's {...}, where the missing required field
			// counts as a match, so "not required" always fails. A false
			// property schema says the same thing without matchN.
			names := absent
			props, _ := out["properties"].(map[string]any)
			if props == nil {
				props = map[string]any{}
				out["properties"] = props
			}
			for _, name := range names {
				props[name] = false
			}
		}
		if presentOK {
			// not: {required: [X], properties: {X: S}} means X is absent
			// or does not match S, which is a not on X's own schema.
			name, schema := presentName, prepareExtension(presentSchema, true, lowercaseEngine, presentName)
			props, _ := out["properties"].(map[string]any)
			if props == nil {
				props = map[string]any{}
				out["properties"] = props
			}
			negated := map[string]any{"not": schema, literalMarker: true}
			if existing, ok := props[name]; ok {
				negated = map[string]any{"allOf": []any{existing, negated}}
			}
			props[name] = negated
		}
		if literal {
			out[literalMarker] = true
		}
		return out
	}
	return v
}

func notPresentMatching(v any) (string, any, bool) {
	n, ok := v.(map[string]any)
	if !ok {
		return "", nil, false
	}
	required := asArray(n["required"])
	props, _ := n["properties"].(map[string]any)
	if len(required) != 1 || len(props) != 1 {
		return "", nil, false
	}
	name, _ := required[0].(string)
	schema, ok := props[name]
	if !ok {
		return "", nil, false
	}
	for k := range n {
		if k != "required" && k != "properties" {
			return "", nil, false
		}
	}
	return name, schema, true
}

// absentNames recognizes not: {required: [X]} and not: {anyOf: [{required:
// [X]}, {required: [Y]}]}, which forbid each named property, and returns the
// names. Property schemas beside them must be true, which constrains nothing.
func absentNames(v any) ([]string, bool) {
	n, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	single := func(x any) (string, bool) {
		names := asArray(x)
		if len(names) != 1 {
			return "", false
		}
		name, ok := names[0].(string)
		return name, ok
	}
	var names []string
	for k, x := range n {
		switch k {
		case "required":
			name, ok := single(x)
			if !ok {
				return nil, false
			}
			names = append(names, name)
		case "anyOf":
			for _, item := range asArray(x) {
				m, ok := item.(map[string]any)
				if !ok {
					return nil, false
				}
				for key := range m {
					if key != "required" {
						return nil, false
					}
				}
				name, ok := single(m["required"])
				if !ok {
					return nil, false
				}
				names = append(names, name)
			}
		case "properties":
			for _, schema := range x.(map[string]any) {
				if schema != true {
					return nil, false
				}
			}
		default:
			return nil, false
		}
	}
	sort.Strings(names)
	return names, len(names) > 0
}

var negativePrefix = regexp.MustCompile(`^\^\(\?!([A-Za-z0-9_-]+)\)\.\*\$$`)

// unsupportedPattern returns the first pattern RE2 cannot compile.
func unsupportedPattern(v any) string {
	switch n := v.(type) {
	case map[string]any:
		for k, x := range n {
			if p, ok := x.(string); ok && k == "pattern" {
				if _, err := regexp.Compile(re2Pattern(p)); err != nil {
					return p
				}
			}
			if found := unsupportedPattern(x); found != "" {
				return found
			}
		}
	case []any:
		for _, x := range n {
			if found := unsupportedPattern(x); found != "" {
				return found
			}
		}
	}
	return ""
}

var numericKeywords = []string{"maximum", "minimum", "exclusiveMaximum", "exclusiveMinimum", "multipleOf"}

func hasNumericKeyword(m map[string]any) bool {
	for _, k := range numericKeywords {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

func allStrings(values []any) bool {
	for _, v := range values {
		if _, ok := v.(string); !ok {
			return false
		}
	}
	return len(values) > 0
}

// looseValues returns values with the spellings cfn-lint's equal() also
// accepts: the decimal string of a number, the number a string spells in
// Python's str() form, and the string forms of a boolean.
func looseValues(values []any) []any {
	var out []any
	seen := map[string]bool{}
	add := func(v any) {
		key := fmt.Sprintf("%T:%v", v, v)
		if !seen[key] {
			seen[key] = true
			out = append(out, v)
		}
	}
	for _, v := range values {
		add(v)
		switch x := v.(type) {
		case float64:
			add(pythonNumber(x))
		case string:
			if strings.EqualFold(x, "true") || strings.EqualFold(x, "false") {
				b := strings.EqualFold(x, "true")
				add(b)
				for _, s := range boolSpellings(b) {
					add(s)
				}
				continue
			}
			if i, err := strconv.ParseInt(x, 10, 64); err == nil && strconv.FormatInt(i, 10) == x {
				add(float64(i))
			} else if f, err := strconv.ParseFloat(x, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) && pythonNumber(f) == x && strings.Contains(x, ".") {
				add(f)
			}
		case bool:
			for _, s := range boolSpellings(x) {
				add(s)
			}
		}
	}
	return out
}

func boolSpellings(b bool) []string {
	if b {
		return []string{"true", "True", "TRUE"}
	}
	return []string{"false", "False", "FALSE"}
}

// pythonNumber formats an integral JSON number as Python's str(int) does and
// a fractional one as str(float) does for ordinary magnitudes.
func pythonNumber(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// dropValidatorStructs removes {...} from an & chain that contains matchIf or
// matchN. The extractor adds it to say the value is an object, but CUE then
// checks the validators against that empty struct while evaluating the
// schema: a then branch requiring a property, or a not whose required
// property {} lacks, makes every resource of the type invalid. The validators
// apply to the resource's object, so the struct adds nothing.
func dropValidatorStructs(f *ast.File) {
	isValidator := func(x ast.Expr) bool {
		call, ok := x.(*ast.CallExpr)
		if !ok {
			return false
		}
		fun, ok := call.Fun.(*ast.Ident)
		return ok && (fun.Name == "matchIf" || fun.Name == "matchN")
	}
	isOpenStruct := func(x ast.Expr) bool {
		s, ok := x.(*ast.StructLit)
		if !ok || len(s.Elts) != 1 {
			return false
		}
		e, ok := s.Elts[0].(*ast.Ellipsis)
		return ok && e.Type == nil
	}
	var operands func(ast.Expr) []ast.Expr
	operands = func(x ast.Expr) []ast.Expr {
		if b, ok := x.(*ast.BinaryExpr); ok && b.Op == token.AND {
			return append(operands(b.X), operands(b.Y)...)
		}
		return []ast.Expr{x}
	}
	// Post-order, so chains nested in matchIf arguments are rewritten too.
	astutil.Apply(f, nil, func(cur astutil.Cursor) bool {
		b, ok := cur.Node().(*ast.BinaryExpr)
		if !ok || b.Op != token.AND {
			return true
		}
		all := operands(b)
		validators := false
		for _, x := range all {
			validators = validators || isValidator(x)
		}
		if !validators {
			return true
		}
		var kept []ast.Expr
		for _, x := range all {
			if !isOpenStruct(x) {
				kept = append(kept, x)
			}
		}
		if len(kept) != len(all) {
			cur.Replace(and(kept...))
		}
		return true
	})
}

// scopedExtensions compiles the Scoped rules for the converter's resource
// and adds them to root, the normalized properties schema.
func (c *converter) scopedExtensions(root map[string]any) error {
	rules := &fieldRules{}
	names := map[string]bool{}
	for _, ext := range cfnLintExtensions {
		paths, ok := ext.Paths[c.resource]
		if !ok || !ext.Scoped {
			continue
		}
		b, err := readExtension(ext.File)
		if err != nil {
			return err
		}
		var schema map[string]any
		if err := json.Unmarshal(b, &schema); err != nil {
			return fmt.Errorf("%s: %w", ext.File, err)
		}
		if pattern := unsupportedPattern(schema); pattern != "" && !negativePrefix.MatchString(pattern) {
			c.warn("#", "cfnLintExtension", ext.Rule+" not applied: pattern is incompatible with RE2: "+pattern)
			continue
		}
		compiler := &ruleCompiler{rule: ext.Rule, message: ext.Message, lowercaseEngine: ext.LowercaseEngine, template: c.template, properties: root}
		for _, path := range paths {
			decls, err := compiler.scopedRule(schema, path)
			if err != nil {
				c.warn("#/"+path, "cfnLintExtension", ext.Rule+" not applied: "+err.Error())
				continue
			}
			if len(decls) > 0 {
				ast.AddComment(decls[0], &ast.CommentGroup{Doc: true, List: []*ast.Comment{{Text: "// cfn-lint " + ext.Rule + ": " + ext.File}}})
			}
			rules.scoped = append(rules.scoped, decls...)
			for name := range compiler.names {
				names[name] = true
			}
		}
	}
	if len(rules.scoped) == 0 {
		return nil
	}
	// applyRules places one set of rules per struct, so join the rules
	// normalize already marked on root.
	props, _ := root["properties"].(map[string]any)
	for name := range props {
		var index int
		if _, e := fmt.Sscanf(name, ruleMarker+"%d", &index); e == nil && index < len(c.rules) && c.rules[index] != nil {
			existing := c.rules[index]
			existing.scoped = append(existing.scoped, rules.scoped...)
			existing.scopedNames = append(existing.scopedNames, sortedBoolKeys(names)...)
			return nil
		}
	}
	rules.scopedNames = sortedBoolKeys(names)
	c.mark(root, rules)
	return nil
}
