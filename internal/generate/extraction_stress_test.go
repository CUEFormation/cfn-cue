package generate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Extraction stress harnesses from the issue #12 investigation. They repeat
// literal and template extraction from the pinned snapshot and fail on any
// error or on output that differs from the first extraction of the same input.
// See corpus/reports/2026-10-14/issue-12-field-rule-placement.md.

// CFN_CUE_STRESS=<iterations> go test ./internal/generate -run TestExtractionStress$
//
// CFN_CUE_STRESS_HASHES selects schema hashes (default GroundStation
// DataflowEndpointGroupV2); CFN_CUE_STRESS_PAR runs that many workers, for use
// with -race.
func TestExtractionStress(t *testing.T) {
	if os.Getenv("CFN_CUE_STRESS") == "" {
		t.Skip("set CFN_CUE_STRESS=<iterations>")
	}
	n, _ := strconv.Atoi(os.Getenv("CFN_CUE_STRESS"))
	par, _ := strconv.Atoi(os.Getenv("CFN_CUE_STRESS_PAR"))
	if par < 1 {
		par = 1
	}
	hashes := strings.Split(os.Getenv("CFN_CUE_STRESS_HASHES"), ",")
	if hashes[0] == "" {
		hashes = []string{"a3fc2798aeb3e8b6"}
	}
	source, e := OpenSource(context.Background(), "../../snapshots/schemas-2026-10-12.zip")
	if e != nil {
		t.Fatal(e)
	}
	for _, hash := range hashes {
		b, e := source.read("resources/" + hash + ".json")
		if e != nil {
			t.Fatal(e)
		}
		var mu sync.Mutex
		first := map[bool]string{}
		var wg sync.WaitGroup
		for w := 0; w < par; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < n; i++ {
					root := map[string]any{}
					if e := json.Unmarshal(b, &root); e != nil {
						t.Error(e)
						return
					}
					typ, _ := root["typeName"].(string)
					c := &converter{module: "m", resource: typ, iam: true, cfnLintRules: true}
					for _, template := range []bool{false, true} {
						sum, e := extractSum(c, root, template)
						if e != nil {
							t.Errorf("%s template=%v iteration %d: %v", hash, template, i, e)
							return
						}
						mu.Lock()
						if first[template] == "" {
							first[template] = sum
						} else if first[template] != sum {
							t.Errorf("%s template=%v iteration %d: output changed", hash, template, i)
						}
						mu.Unlock()
					}
				}
			}()
		}
		wg.Wait()
	}
}

// CFN_CUE_STRESS_ALL=<rounds> go test ./internal/generate -run TestExtractionStressAll
//
// Each round extracts every schema in a new shuffled order (seeded by
// CFN_CUE_STRESS_SEED), so CUE's process-wide state differs each time a schema
// is reached. Odd rounds enable LanguageExtensions.
func TestExtractionStressAll(t *testing.T) {
	if os.Getenv("CFN_CUE_STRESS_ALL") == "" {
		t.Skip("set CFN_CUE_STRESS_ALL=<rounds>")
	}
	rounds, _ := strconv.Atoi(os.Getenv("CFN_CUE_STRESS_ALL"))
	seed, _ := strconv.ParseInt(os.Getenv("CFN_CUE_STRESS_SEED"), 10, 64)
	source, e := OpenSource(context.Background(), "../../snapshots/schemas-2026-10-12.zip")
	if e != nil {
		t.Fatal(e)
	}
	var hashes []string
	data := map[string][]byte{}
	for name, b := range source.files {
		if h, ok := strings.CutPrefix(name, "resources/"); ok && strings.HasSuffix(h, ".json") {
			h = strings.TrimSuffix(h, ".json")
			hashes = append(hashes, h)
			data[h] = b
		}
	}
	sort.Strings(hashes)
	t.Logf("%d schemas", len(hashes))
	rng := rand.New(rand.NewSource(seed))
	first := map[string]string{}
	for r := 0; r < rounds; r++ {
		rng.Shuffle(len(hashes), func(i, j int) { hashes[i], hashes[j] = hashes[j], hashes[i] })
		ext := r%2 == 1
		for _, h := range hashes {
			root := map[string]any{}
			if e := json.Unmarshal(data[h], &root); e != nil {
				t.Fatal(e)
			}
			typ, _ := root["typeName"].(string)
			c := &converter{module: "m", resource: typ, extensions: ext, iam: true, cfnLintRules: true}
			for _, template := range []bool{false, true} {
				sum, e := extractSum(c, root, template)
				if e != nil {
					t.Errorf("round %d %s %s template=%v: %v", r, h, typ, template, e)
					continue
				}
				key := fmt.Sprintf("%s/%v/%v", h, template, ext)
				if first[key] == "" {
					first[key] = sum
				} else if first[key] != sum {
					t.Errorf("round %d %s template=%v ext=%v: output changed", r, typ, template, ext)
				}
			}
		}
		t.Logf("round %d done", r)
	}
}

func extractSum(c *converter, root map[string]any, template bool) (string, error) {
	f, e := c.extract(root, template)
	if e != nil {
		return "", e
	}
	out, e := render(f)
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(out)
	return hex.EncodeToString(sum[:]), nil
}
