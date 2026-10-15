package generate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
)

// TestScopedRulesOnProviderSchemas generates the resource types of the
// scoped cfn-lint rules from the checked-in snapshot and checks each rule.
// Expected results match cfn-lint v1.57.2 on the same properties, except
// where noted.
func TestScopedRulesOnProviderSchemas(t *testing.T) {
	source, err := OpenSource(context.Background(), "../../snapshots/schemas-2026-10-12.zip")
	if err != nil {
		t.Fatal(err)
	}
	types := []string{"AWS::DynamoDB::Table", "AWS::ECS::TaskDefinition", "AWS::RDS::DBCluster", "AWS::RDS::DBInstance"}
	out := filepath.Join(t.TempDir(), "generated")
	manifest, err := Generate(source, Options{Out: out, Regions: []string{"us-east-1"}, Resources: types})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range manifest.Diagnostics {
		if d.Keyword == "cfnLintExtension" {
			t.Errorf("rule not applied: %v", d)
		}
	}
	schemas := map[string]cue.Value{}
	for _, typ := range types {
		schemas[typ] = build(t, out, "./resources/h"+manifest.Regions["us-east-1"][typ]).LookupPath(cue.MakePath(cue.Def("Resource")))
	}
	keys := `KeySchema:[{AttributeName:"id",KeyType:"HASH"}],AttributeDefinitions:[{AttributeName:"id",AttributeType:"S"}]`
	fargate := `RequiresCompatibilities:["FARGATE"],NetworkMode:"awsvpc",ContainerDefinitions:[{Name:"app",Image:"nginx"}]`
	for _, tt := range []struct {
		typ, properties string
		failure         string // empty for a valid resource
	}{
		// E3638: PAY_PER_REQUEST capacity must be 0.
		{"AWS::DynamoDB::Table", keys + `,BillingMode:"PAY_PER_REQUEST"`, ""},
		{"AWS::DynamoDB::Table", keys + `,BillingMode:"PAY_PER_REQUEST",ProvisionedThroughput:{ReadCapacityUnits:0,WriteCapacityUnits:0}`, ""},
		{"AWS::DynamoDB::Table", keys + `,BillingMode:"PAY_PER_REQUEST",ProvisionedThroughput:{ReadCapacityUnits:5,WriteCapacityUnits:5}`, "E3638"},
		{"AWS::DynamoDB::Table", keys + `,BillingMode:"PAY_PER_REQUEST",ProvisionedThroughput:{ReadCapacityUnits:"0",WriteCapacityUnits:"0"}`, ""},
		{"AWS::DynamoDB::Table", keys + `,BillingMode:"PAY_PER_REQUEST",ProvisionedThroughput:{ReadCapacityUnits:"5",WriteCapacityUnits:0}`, "E3638"},
		{"AWS::DynamoDB::Table", keys + `,BillingMode:"PAY_PER_REQUEST",ProvisionedThroughput:{ReadCapacityUnits:{Ref:"N"},WriteCapacityUnits:0}`, ""},
		// E3639: PROVISIONED, or no BillingMode, needs capacity of at least 1.
		{"AWS::DynamoDB::Table", keys + `,BillingMode:"PROVISIONED",ProvisionedThroughput:{ReadCapacityUnits:5,WriteCapacityUnits:5}`, ""},
		{"AWS::DynamoDB::Table", keys + `,ProvisionedThroughput:{ReadCapacityUnits:5,WriteCapacityUnits:5}`, ""},
		{"AWS::DynamoDB::Table", keys + `,BillingMode:"PROVISIONED"`, "ProvisionedThroughput: field is required"},
		{"AWS::DynamoDB::Table", keys, "ProvisionedThroughput: field is required"},
		{"AWS::DynamoDB::Table", keys + `,BillingMode:"PROVISIONED",ProvisionedThroughput:{ReadCapacityUnits:0,WriteCapacityUnits:5}`, "E3639"},
		{"AWS::DynamoDB::Table", keys + `,BillingMode:"PROVISIONED",ProvisionedThroughput:{ReadCapacityUnits:"0",WriteCapacityUnits:"5"}`, "E3639"},
		{"AWS::DynamoDB::Table", keys + `,BillingMode:"PROVISIONED",ProvisionedThroughput:{ReadCapacityUnits:"5",WriteCapacityUnits:"5"}`, ""},
		{"AWS::DynamoDB::Table", keys + `,BillingMode:"PROVISIONED",ProvisionedThroughput:{ReadCapacityUnits:{Ref:"N"},WriteCapacityUnits:5}`, ""},
		// An intrinsic BillingMode satisfies neither condition.
		{"AWS::DynamoDB::Table", keys + `,BillingMode:{Ref:"Mode"}`, ""},
		{"AWS::DynamoDB::Table", keys + `,BillingMode:{"Fn::If":["C","PROVISIONED","PAY_PER_REQUEST"]},ProvisionedThroughput:{"Fn::If":["C",{ReadCapacityUnits:5,WriteCapacityUnits:5},{Ref:"AWS::NoValue"}]}`, ""},

		// E3042: at least one essential container.
		{"AWS::ECS::TaskDefinition", `ContainerDefinitions:[{Name:"a",Image:"i",Essential:false},{Name:"b",Image:"i"}]`, ""},
		{"AWS::ECS::TaskDefinition", `ContainerDefinitions:[{Name:"a",Image:"i",Essential:false}]`, "E3042"},
		{"AWS::ECS::TaskDefinition", `ContainerDefinitions:[{Name:"a",Image:"i",Essential:"false"}]`, "E3042"},
		{"AWS::ECS::TaskDefinition", `ContainerDefinitions:[{Name:"a",Image:"i",Essential:"true"}]`, ""},
		{"AWS::ECS::TaskDefinition", `ContainerDefinitions:[{Name:"a",Image:"i",Essential:{Ref:"E"}}]`, ""},
		{"AWS::ECS::TaskDefinition", `ContainerDefinitions:[]`, "E3042"},
		{"AWS::ECS::TaskDefinition", `ContainerDefinitions:{Ref:"Containers"}`, ""},
		// E3047: Fargate Cpu and Memory combinations.
		{"AWS::ECS::TaskDefinition", fargate + `,Cpu:256,Memory:512`, ""},
		{"AWS::ECS::TaskDefinition", fargate + `,Cpu:"256",Memory:"512"`, ""},
		{"AWS::ECS::TaskDefinition", fargate + `,Cpu:256,Memory:4096`, "E3047"},
		{"AWS::ECS::TaskDefinition", fargate + `,Cpu:"512",Memory:"3072"`, ""},
		{"AWS::ECS::TaskDefinition", fargate + `,Cpu:"512",Memory:"3000"`, "E3047"},
		{"AWS::ECS::TaskDefinition", fargate + `,Cpu:"512",Memory:"5000"`, "E3047"},
		{"AWS::ECS::TaskDefinition", fargate + `,Cpu:".25 vCPU",Memory:"1GB"`, ""},
		{"AWS::ECS::TaskDefinition", fargate + `,Cpu:".25 vCPU",Memory:"3GB"`, "E3047"},
		{"AWS::ECS::TaskDefinition", fargate + `,Cpu:{Ref:"Cpu"},Memory:{Ref:"Memory"}`, ""},
		{"AWS::ECS::TaskDefinition", `RequiresCompatibilities:["EC2"],ContainerDefinitions:[{Name:"a",Image:"i"}],Cpu:100,Memory:100`, ""},
		{"AWS::ECS::TaskDefinition", `RequiresCompatibilities:{Ref:"Compatibilities"},ContainerDefinitions:[{Name:"a",Image:"i"}],Cpu:100,Memory:100`, ""},
		// E3048: Fargate network mode, sizing, log driver and placement.
		{"AWS::ECS::TaskDefinition", fargate + `,Cpu:300,Memory:512`, "E3048"},
		{"AWS::ECS::TaskDefinition", strings.Replace(fargate, `"awsvpc"`, `"bridge"`, 1) + `,Cpu:256,Memory:512`, "E3048"},
		{"AWS::ECS::TaskDefinition", strings.Replace(fargate, `"awsvpc"`, `{Ref:"Mode"}`, 1) + `,Cpu:256,Memory:512`, ""},
		{"AWS::ECS::TaskDefinition", strings.Replace(fargate, `NetworkMode:"awsvpc",`, ``, 1) + `,Cpu:256,Memory:512`, "NetworkMode: field is required"},
		{"AWS::ECS::TaskDefinition", fargate, "Cpu: field is required"},
		{"AWS::ECS::TaskDefinition", fargate + `,Cpu:256,Memory:512,PlacementConstraints:[{Type:"memberOf",Expression:"x"}]`, "E3048"},
		{"AWS::ECS::TaskDefinition", `RequiresCompatibilities:["FARGATE"],NetworkMode:"awsvpc",Cpu:256,Memory:512,ContainerDefinitions:[{Name:"a",Image:"i",LogConfiguration:{LogDriver:"syslog"}}]`, "E3048"},
		{"AWS::ECS::TaskDefinition", `RequiresCompatibilities:["FARGATE"],NetworkMode:"awsvpc",Cpu:256,Memory:512,ContainerDefinitions:[{Name:"a",Image:"i",LogConfiguration:{LogDriver:{Ref:"Driver"}}}]`, ""},

		// E3690: DBCluster engines and versions; Engine is not lowercased.
		{"AWS::RDS::DBCluster", `Engine:"aurora-mysql",EngineVersion:"8.0.mysql_aurora.3.04.0"`, ""},
		{"AWS::RDS::DBCluster", `Engine:"aurora-mysql",EngineVersion:"9.9"`, "E3690"},
		{"AWS::RDS::DBCluster", `Engine:"aurora"`, "E3690"},
		{"AWS::RDS::DBCluster", `Engine:"Aurora-MySQL",EngineVersion:"8.0"`, "E3690"},
		// cfn-lint compares str(8.0), so the float matches "8.0" and the
		// integer 8 does not.
		{"AWS::RDS::DBCluster", `Engine:"aurora-mysql",EngineVersion:8.0`, ""},
		{"AWS::RDS::DBCluster", `Engine:"aurora-mysql",EngineVersion:8`, "E3690"},
		{"AWS::RDS::DBCluster", `Engine:"aurora-postgresql",EngineVersion:{Ref:"Version"}`, ""},
		// E3691: DBInstance; Engine is compared in lowercase.
		{"AWS::RDS::DBInstance", `Engine:"mysql",EngineVersion:"8.0.35"`, ""},
		{"AWS::RDS::DBInstance", `Engine:"MySQL",EngineVersion:"8.0.35"`, ""},
		{"AWS::RDS::DBInstance", `Engine:"mysql",EngineVersion:"5.0.1"`, "E3691"},
		{"AWS::RDS::DBInstance", `Engine:"notanengine"`, "E3691"},
		{"AWS::RDS::DBInstance", `Engine:"postgres",EngineVersion:16.1`, ""},
		{"AWS::RDS::DBInstance", `Engine:"mysql",EngineVersion:8.0`, "E3691"},
		{"AWS::RDS::DBInstance", `Engine:{Ref:"Engine"},EngineVersion:"1.2.3"`, ""},
		{"AWS::RDS::DBInstance", `Engine:"mysql",EngineVersion:{Ref:"Version"}`, ""},
		// cfn-lint reports E3691 here. As for other constraints, a dynamic
		// reference is resolved by CloudFormation before it is checked.
		{"AWS::RDS::DBInstance", `Engine:"mysql",EngineVersion:"{{resolve:ssm:/rds/version}}"`, ""},
	} {
		schema := schemas[tt.typ]
		data := `{Type:"` + tt.typ + `",Properties:{` + tt.properties + `}}`
		v := schema.Context().CompileString(data)
		if err := v.Err(); err != nil {
			t.Fatalf("invalid test input %s: %v", data, err)
		}
		err := schema.Unify(v).Validate(cue.Concrete(true))
		switch {
		case tt.failure == "" && err != nil:
			t.Errorf("%s: unexpected error: %v", data, err)
		case tt.failure != "" && err == nil:
			t.Errorf("%s: valid, want %s", data, tt.failure)
		case tt.failure != "" && !strings.Contains(errorDetail(err), tt.failure):
			t.Errorf("%s: want %s, got %v", data, tt.failure, errorDetail(err))
		}
	}
}

func errorDetail(err error) string { return cueerrors.Details(err, nil) }

// TestScopedRuleDiagnostics checks that a scoped rule the compiler cannot
// express is reported and skipped rather than applied in part.
func TestScopedRuleDiagnostics(t *testing.T) {
	savedRules, savedRead := cfnLintExtensions, readExtension
	defer func() { cfnLintExtensions, readExtension = savedRules, savedRead }()
	files := map[string]string{
		"keyword.json": `{"if":{"required":["Name"]},"then":{"properties":{"Names":{"uniqueItems":true}}}}`,
		"missing.json": `{"if":{"required":["Absent"]},"then":{"required":["Name"]}}`,
		"applied.json": `{"if":{"properties":{"Name":{"const":"two"}},"required":["Name"]},"then":{"properties":{"Count":{"maximum":2}}}}`,
	}
	readExtension = func(name string) ([]byte, error) { return []byte(files[name]), nil }
	thing := map[string][]string{"AWS::Test::Thing": {""}}
	cfnLintExtensions = []extension{
		{Rule: "E0001", File: "keyword.json", Paths: thing, Scoped: true, Message: "keyword"},
		{Rule: "E0002", File: "missing.json", Paths: thing, Scoped: true, Message: "missing"},
		{Rule: "E0003", File: "applied.json", Paths: thing, Scoped: true, Message: "at most 2 when two"},
	}
	out := filepath.Join(t.TempDir(), "generated")
	manifest, err := Generate(fixture(t), Options{Out: out, Regions: []string{"us-east-1"}})
	if err != nil {
		t.Fatal(err)
	}
	skipped := map[string]bool{}
	for _, d := range manifest.Diagnostics {
		if d.Keyword == "cfnLintExtension" {
			skipped[strings.Fields(d.Detail)[0]] = true
		}
	}
	if !skipped["E0001"] || !skipped["E0002"] || skipped["E0003"] {
		t.Errorf("skipped rules = %v, want E0001 and E0002", skipped)
	}
	schema := propertiesSchema(t, out, "./resources/htest")
	validate(t, schema, `{Name:"one",Count:3}`, true)
	validate(t, schema, `{Name:"two",Count:2}`, true)
	validate(t, schema, `{Name:"two",Count:"2"}`, true)
	validate(t, schema, `{Name:"two",Count:{Ref:"Count"}}`, true)
	validate(t, schema, `{Name:"two",Count:3}`, false)
	validate(t, schema, `{Name:"two",Count:"3"}`, false)
}
