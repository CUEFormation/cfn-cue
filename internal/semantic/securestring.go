package semantic

import (
	"regexp"
	"strconv"
	"strings"
)

// secureStringProperties are the resource properties where CloudFormation
// resolves {{resolve:ssm-secure:...}}, as cfn-lint 1.57.2 lists them for
// E1027. Each is a resource type and the property path below Properties; "*"
// is any list item.
var secureStringProperties = map[string]bool{
	"AWS::DirectoryService::MicrosoftAD/Password":                                    true,
	"AWS::DirectoryService::SimpleAD/Password":                                       true,
	"AWS::ElastiCache::ReplicationGroup/AuthToken":                                   true,
	"AWS::IAM::User/LoginProfile/Password":                                           true,
	"AWS::KinesisFirehose::DeliveryStream/RedshiftDestinationConfiguration/Password": true,
	"AWS::OpsWorks::App/AppSource/Password":                                          true,
	"AWS::OpsWorks::Stack/RdsDbInstances/*/DbPassword":                               true,
	"AWS::OpsWorks::Stack/CustomCookbooksSource/Password":                            true,
	"AWS::RDS::DBCluster/MasterUserPassword":                                         true,
	"AWS::RDS::DBInstance/MasterUserPassword":                                        true,
	"AWS::Redshift::Cluster/MasterUserPassword":                                      true,
}

// dynamicReference is cfn-lint's REGEX_DYN_REF with Python's $, which also
// matches before a final newline. Like cfn-lint, it reads one line, and its
// greedy prefix selects the last reference in the string.
var dynamicReference = regexp.MustCompile(`^.*\{\{(resolve:.+)\}\}.*\n?$`)

// opaqueFunctions are the intrinsics whose arguments cfn-lint does not check
// for dynamic references. Fn::If is transparent: each branch is a value of
// the enclosing property. Condition functions such as Fn::Equals are not
// intrinsics here, so their arguments are checked.
var opaqueFunctions = map[string]bool{
	"Ref": true, "Fn::Base64": true, "Fn::Cidr": true, "Fn::Contains": true,
	"Fn::FindInMap": true, "Fn::GetAtt": true, "Fn::GetAZs": true,
	"Fn::GetStackOutput": true, "Fn::ImportValue": true, "Fn::Join": true,
	"Fn::Length": true, "Fn::Select": true, "Fn::Split": true, "Fn::Sub": true,
	"Fn::ToJsonString": true, "Fn::Transform": true,
}

// secureStrings reports ssm-secure dynamic references outside the listed
// properties. Like cfn-lint, it checks every section except the template's
// Metadata. Properties of a resource without a Type are skipped, since their
// property paths are unknown.
func (c *checker) secureStrings(template map[string]any) {
	resources, _ := template["Resources"].(map[string]any)
	for _, section := range sortedKeys(template) {
		switch section {
		case "Metadata":
		case "Resources":
			for _, name := range sortedKeys(resources) {
				r, _ := resources[name].(map[string]any)
				typ, _ := r["Type"].(string)
				for _, key := range sortedKeys(r) {
					path := []string{"Resources", name, key}
					if key != "Properties" {
						c.secureValue(r[key], path, nil)
					} else if typ != "" {
						c.secureValue(r[key], path, []string{typ})
					}
				}
			}
		default:
			c.secureValue(template[section], []string{section}, nil)
		}
	}
}

// secureValue walks one value. path is the JSON path; property is the type
// and property path below Properties, or nil outside resource properties.
func (c *checker) secureValue(v any, path, property []string) {
	child := func(p []string, s string) []string { return append(p[:len(p):len(p)], s) }
	switch v := v.(type) {
	case string:
		if !strings.Contains(v, "{{resolve:") {
			return
		}
		// cfn-lint skips a string when any key on its path is an opaque
		// function name, even a logical ID or a key in a larger object.
		for _, key := range path {
			if opaqueFunctions[key] {
				return
			}
		}
		m := dynamicReference.FindStringSubmatch(v)
		if m == nil {
			return
		}
		// cfn-lint's E1050 requires resolve:<service>:<rest> before E1027 runs.
		if parts := strings.Split(m[1], ":"); len(parts) < 3 || parts[1] != "ssm-secure" {
			return
		}
		if property != nil && secureStringProperties[strings.Join(property, "/")] {
			return
		}
		where := "outside resource properties"
		if property != nil {
			where = "in " + property[0] + " property " + strings.Join(property[1:], "/")
		}
		c.report("ssm-secure-placement", Error, path, nil,
			"dynamic reference {{%s}} is %s; CloudFormation resolves ssm-secure only in %d resource properties, such as AWS::RDS::DBInstance MasterUserPassword",
			m[1], where, len(secureStringProperties))
	case []any:
		for i, x := range v {
			var p []string
			if property != nil {
				p = child(property, "*")
			}
			c.secureValue(x, child(path, strconv.Itoa(i)), p)
		}
	case map[string]any:
		if args, ok := v["Fn::If"]; ok && len(v) == 1 {
			c.secureIf(args, child(path, "Fn::If"), property)
			return
		}
		for _, key := range sortedKeys(v) {
			var p []string
			if property != nil {
				p = child(property, key)
			}
			c.secureValue(v[key], child(path, key), p)
		}
	}
}

// secureIf walks Fn::If as cfn-lint does. The condition name is checked
// against the enclosing property. The branches keep that property path, and
// are walked only when the function is well formed and its condition is
// defined.
func (c *checker) secureIf(v any, path, property []string) {
	args, ok := v.([]any)
	if !ok || len(args) != 3 {
		return
	}
	name, ok := args[0].(string)
	if !ok {
		return
	}
	c.secureValue(name, append(path[:len(path):len(path)], "0"), property)
	if !c.m.Conditions.Defined(name) {
		return
	}
	for i := 1; i <= 2; i++ {
		c.secureValue(args[i], append(path[:len(path):len(path)], strconv.Itoa(i)), property)
	}
}
