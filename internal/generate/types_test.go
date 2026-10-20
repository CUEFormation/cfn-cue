package generate

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/cueformation/cfn-cue/internal/bundle"
)

func TestBuildTypes(t *testing.T) {
	regions := map[string]map[string]string{
		"us-east-1": {"AWS::T::All": "a", "AWS::T::Rare": "r", "AWS::T::Most": "m", "AWS::T::Half": "h"},
		"us-east-2": {"AWS::T::All": "a", "AWS::T::Most": "m", "AWS::T::Half": "h"},
		"us-west-1": {"AWS::T::All": "a", "AWS::T::Most": "m"},
		"us-west-2": {"AWS::T::All": "a"},
	}
	names, types := buildTypes(regions)
	if want := []string{"us-east-1", "us-east-2", "us-west-1", "us-west-2"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("regions %v, want %v", names, want)
	}
	for typ, want := range map[string]string{
		"AWS::T::All":  `{"hash":"a","regions":"*"}`,
		"AWS::T::Rare": `{"hash":"r","regions":["us-east-1"]}`,
		"AWS::T::Most": `{"hash":"m","exceptRegions":["us-west-2"]}`,
		"AWS::T::Half": `{"hash":"h","regions":["us-east-1","us-east-2"]}`,
	} {
		b, err := json.Marshal(types[typ])
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != want {
			t.Errorf("%s: %s, want %s", typ, b, want)
		}
	}
	if len(types) != 4 {
		t.Errorf("%d types, want 4", len(types))
	}
	b, err := json.Marshal(bundle.Manifest{Regions: names, Types: types})
	if err != nil {
		t.Fatal(err)
	}
	var m bundle.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for region, want := range regions {
		if got := m.RegionTypes(region); !reflect.DeepEqual(got, want) {
			t.Errorf("%s types %v, want %v", region, got, want)
		}
	}
	if got := m.RegionTypes("eu-west-1"); len(got) != 0 {
		t.Errorf("unknown region has types %v", got)
	}
	if _, ok := m.SchemaHash("AWS::T::Rare", "us-east-2"); ok {
		t.Error("AWS::T::Rare reported in us-east-2")
	}
	if hash, ok := m.SchemaHash("AWS::T::Most", "us-west-1"); !ok || hash != "m" {
		t.Errorf("AWS::T::Most in us-west-1: %q %v", hash, ok)
	}
	var info bundle.TypeInfo
	if err := json.Unmarshal([]byte(`{"hash":"a","regions":"us-east-1"}`), &info); err == nil {
		t.Error("regions string other than * accepted")
	}
}
