package generate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cuelang.org/go/cue"

	"github.com/cueformation/cfn-cue/internal/bundle"
)

// perfCases are minimal valid resources. Cost is dominated by the schema,
// not by the properties set, so small inputs show the per-resource floor.
var perfCases = []struct{ typ, properties string }{
	{"AWS::SQS::Queue", `{QueueName:"q"}`},
	{"AWS::IAM::Role", `{AssumeRolePolicyDocument:{Statement:[{Effect:"Allow",Principal:{Service:"lambda.amazonaws.com"},Action:"sts:AssumeRole"}]}}`},
	{"AWS::EC2::Subnet", `{VpcId:"vpc-0123456789abcdef0",CidrBlock:"10.0.1.0/24"}`},
	{"AWS::S3::Bucket", `{BucketName:"bucket"}`},
	{"AWS::Lambda::Function", `{Code:{ZipFile:"x"},Role:{"Fn::GetAtt":["Role","Arn"]},Handler:"index.handler",Runtime:"python3.12"}`},
	{"AWS::DynamoDB::Table", `{KeySchema:[{AttributeName:"id",KeyType:"HASH"}],AttributeDefinitions:[{AttributeName:"id",AttributeType:"S"}],BillingMode:"PAY_PER_REQUEST"}`},
}

// perfBudget is the target validation time per resource instance.
const perfBudget = 100 * time.Millisecond

// TestResourcePerformance reports load time and per-instance validation time
// for shared resource packages in ../../generated. Set CFN_CUE_PERF=1 to run;
// CFN_CUE_PERF_N sets instances per type (default 5) and
// CFN_CUE_PERF_ENFORCE=1 fails types over perfBudget.
func TestResourcePerformance(t *testing.T) {
	if os.Getenv("CFN_CUE_PERF") != "1" {
		t.Skip("set CFN_CUE_PERF=1 to measure generated resource schemas")
	}
	n := 5
	if s := os.Getenv("CFN_CUE_PERF_N"); s != "" {
		var e error
		if n, e = strconv.Atoi(s); e != nil || n < 1 {
			t.Fatalf("CFN_CUE_PERF_N: %q", s)
		}
	}
	dir, e := filepath.Abs("../../generated")
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	var m bundle.Manifest
	if e = json.Unmarshal(b, &m); e != nil {
		t.Fatal(e)
	}
	for _, c := range perfCases {
		hash, ok := m.SchemaHash(c.typ, "us-east-1")
		if !ok {
			t.Fatalf("%s is not in us-east-1", c.typ)
		}
		start := time.Now()
		schema := build(t, dir, "./resources/h"+hash).LookupPath(cue.MakePath(cue.Def("Resource")))
		load := time.Since(start)
		start = time.Now()
		for i := 0; i < n; i++ {
			data := schema.Context().CompileString(fmt.Sprintf(`{Type:%q,Properties:%s,Metadata:{Instance:%d}}`, c.typ, c.properties, i))
			if e := schema.Unify(data).Validate(cue.Concrete(true)); e != nil {
				t.Fatalf("%s: %v", c.typ, e)
			}
		}
		each := time.Since(start) / time.Duration(n)
		t.Logf("%-24s load %7s  per resource %7s", c.typ, load.Round(time.Millisecond), each.Round(time.Millisecond))
		if os.Getenv("CFN_CUE_PERF_ENFORCE") == "1" && each > perfBudget {
			t.Errorf("%s: %s per resource exceeds %s", c.typ, each.Round(time.Millisecond), perfBudget)
		}
	}
}

// TestRegionPerformance validates templates through regions/us-east-1
// #Template in the module at CFN_CUE_PERF_DIR. CFN_CUE_PERF_SIZES lists
// template sizes (default 1,10,50). CFN_CUE_PERF_IF_DEPTH wraps each
// resource's Properties in that many nested Fn::If (default 0). Each size is
// checked valid, and with its first resource broken. Results are logged as
// RESULT lines.
func TestRegionPerformance(t *testing.T) {
	dir := os.Getenv("CFN_CUE_PERF_DIR")
	if dir == "" {
		t.Skip("set CFN_CUE_PERF_DIR to a generated module")
	}
	sizes := []int{1, 10, 50}
	if s := os.Getenv("CFN_CUE_PERF_SIZES"); s != "" {
		sizes = nil
		for _, f := range strings.Split(s, ",") {
			n, e := strconv.Atoi(f)
			if e != nil || n < 1 {
				t.Fatalf("CFN_CUE_PERF_SIZES: %q", s)
			}
			sizes = append(sizes, n)
		}
	}
	depth := 0
	if s := os.Getenv("CFN_CUE_PERF_IF_DEPTH"); s != "" {
		var e error
		if depth, e = strconv.Atoi(s); e != nil || depth < 0 {
			t.Fatalf("CFN_CUE_PERF_IF_DEPTH: %q", s)
		}
	}
	b, e := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	types, e := regionTypeCount(b, "us-east-1")
	if e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	schema := build(t, dir, "./regions/us-east-1").LookupPath(cue.MakePath(cue.Def("Template")))
	t.Logf("RESULT types=%d load_ms=%d", types, time.Since(start).Milliseconds())
	for _, n := range sizes {
		for _, broken := range []bool{false, true} {
			var r strings.Builder
			for i := 0; i < n; i++ {
				c := perfCases[i%len(perfCases)]
				properties := c.properties
				if broken && i == 0 {
					properties = `{QueueName:[]}`
				}
				// The literal leaf is innermost, so it is checked after
				// every enclosing Fn::If.
				for d := 0; d < depth; d++ {
					properties = fmt.Sprintf(`{"Fn::If":["C%d",%s,{Ref:"AWS::NoValue"}]}`, d, properties)
				}
				fmt.Fprintf(&r, "R%d:{Type:%q,Properties:%s},", i, c.typ, properties)
			}
			data := schema.Context().CompileString("{Resources:{" + r.String() + "}}")
			start := time.Now()
			e := schema.Unify(data).Validate(cue.Concrete(true))
			elapsed := time.Since(start)
			if (e == nil) == broken {
				t.Fatalf("types=%d n=%d broken=%v: unexpected result: %v", types, n, broken, e)
			}
			t.Logf("RESULT types=%d if_depth=%d n=%d broken=%v ms=%d per_resource_us=%d", types, depth, n, broken, elapsed.Milliseconds(), elapsed.Microseconds()/int64(n))
		}
	}
}

// regionTypeCount reads format 1 manifests too, because region-bench.sh runs
// TestRegionPerformance against modules from older generators.
func regionTypeCount(manifest []byte, region string) (int, error) {
	var m bundle.Manifest
	if json.Unmarshal(manifest, &m) == nil {
		return len(m.RegionTypes(region)), nil
	}
	var old struct{ Regions map[string]map[string]string }
	if e := json.Unmarshal(manifest, &old); e != nil {
		return 0, e
	}
	return len(old.Regions[region]), nil
}
