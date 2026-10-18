package generate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync"
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

// iamPolicyBudget bounds TestIAMPolicyPerformance. On an Apple M4, iam
// profiles built on matchN took 2.4 s, allocated 2.3 GiB and peaked at
// 2.1 GiB of heap.
var iamPolicyBudget = struct {
	elapsed         time.Duration
	allocated, peak uint64
}{elapsed: 1500 * time.Millisecond, allocated: 1 << 30, peak: 768 << 20}

// TestIAMPolicyPerformance validates a template of ten
// AWS::IAM::ManagedPolicy resources, each with 30 statements of 40 actions,
// 40 resources and a condition, and fails over iamPolicyBudget. Set
// CFN_CUE_PERF=1 to run. Run it alone: peak heap is sampled for the whole
// process.
func TestIAMPolicyPerformance(t *testing.T) {
	if os.Getenv("CFN_CUE_PERF") != "1" {
		t.Skip("set CFN_CUE_PERF=1 to measure IAM policy validation")
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
	const typ = "AWS::IAM::ManagedPolicy"
	hash, ok := m.SchemaHash(typ, "us-east-1")
	if !ok {
		t.Fatalf("%s is not in us-east-1", typ)
	}
	schema := build(t, dir, "./resources/h"+hash).LookupPath(cue.MakePath(cue.Def("Resource")))
	// One value holds every resource, as in cfn-cue-validate, so peak heap
	// covers the whole template rather than one resource at a time.
	template := schema.Context().CompileString("{}")
	resources := map[string]any{}
	for r := 0; r < 10; r++ {
		name := fmt.Sprintf("Policy%d", r)
		template = template.FillPath(cue.MakePath(cue.Str(name)), schema)
		resources[name] = map[string]any{"Type": typ, "Properties": map[string]any{"PolicyDocument": iamPolicy(r, 30, 40)}}
	}
	data, e := json.Marshal(resources)
	if e != nil {
		t.Fatal(e)
	}
	input := schema.Context().CompileBytes(data)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	peak, stop := samplePeakHeap()
	start := time.Now()
	if e := template.Unify(input).Validate(cue.Concrete(true)); e != nil {
		t.Fatal(e)
	}
	elapsed := time.Since(start)
	stop()
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("RESULT ms=%d alloc_mib=%d peak_heap_mib=%d", elapsed.Milliseconds(), allocated>>20, *peak>>20)
	if elapsed > iamPolicyBudget.elapsed {
		t.Errorf("validation took %s, budget %s", elapsed.Round(time.Millisecond), iamPolicyBudget.elapsed)
	}
	if allocated > iamPolicyBudget.allocated {
		t.Errorf("validation allocated %d MiB, budget %d MiB", allocated>>20, iamPolicyBudget.allocated>>20)
	}
	if *peak > iamPolicyBudget.peak {
		t.Errorf("peak heap %d MiB, budget %d MiB", *peak>>20, iamPolicyBudget.peak>>20)
	}
}

// iamPolicy returns an identity policy document with n statements of k
// actions and k resources each. One resource per statement is an intrinsic.
func iamPolicy(r, n, k int) map[string]any {
	statements := make([]any, n)
	for s := range statements {
		actions := make([]any, k)
		resources := make([]any, k)
		for i := range actions {
			actions[i] = fmt.Sprintf("svc%d:Action%d*", i%7, i)
			resources[i] = fmt.Sprintf("arn:aws:s3:::bucket-%d-%d/key/%d", r, s, i)
		}
		resources[0] = map[string]any{"Fn::Sub": "arn:${AWS::Partition}:s3:::bucket-" + strconv.Itoa(r)}
		statements[s] = map[string]any{
			"Sid":      fmt.Sprintf("S%dx%d", r, s),
			"Effect":   "Allow",
			"Action":   actions,
			"Resource": resources,
			"Condition": map[string]any{
				"StringEquals": map[string]any{"aws:RequestedRegion": []any{"us-east-1", "us-west-2"}},
				"Bool":         map[string]any{"aws:SecureTransport": "true"},
			},
		}
	}
	return map[string]any{"Version": "2012-10-17", "Statement": statements}
}

// samplePeakHeap records the largest heap object size seen every
// millisecond until stop returns.
func samplePeakHeap() (*uint64, func()) {
	var peak uint64
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			metrics.Read(sample)
			peak = max(peak, sample[0].Value.Uint64())
			select {
			case <-done:
				return
			case <-tick.C:
			}
		}
	}()
	return &peak, func() { close(done); wg.Wait() }
}
