package conditions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func model(t *testing.T, template string) *Model {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader([]byte(template)))
	decoder.UseNumber()
	var decoded map[string]any
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return New(decoded)
}

func TestCombinations(t *testing.T) {
	m := model(t, `{
"Parameters":{"Env":{"Type":"String","AllowedValues":["prod","dev"]},"Port":{"Type":"Number","AllowedValues":[80,443]},"Free":{"Type":"String"},"List":{"Type":"CommaDelimitedList","AllowedValues":["a"]}},
"Conditions":{
 "Prod":{"Fn::Equals":[{"Ref":"Env"},"prod"]},
 "Dev":{"Fn::Equals":["dev",{"Ref":"Env"}]},
 "NotProd":{"Fn::Not":[{"Condition":"Prod"}]},
 "Both":{"Fn::And":[{"Condition":"Prod"},{"Condition":"Dev"}]},
 "Either":{"Fn::Or":[{"Condition":"Prod"},{"Condition":"Dev"}]},
 "Http":{"Fn::Equals":[{"Ref":"Port"},"80"]},
 "HttpNumber":{"Fn::Equals":[80,{"Ref":"Port"}]},
 "FreeA":{"Fn::Equals":[{"Ref":"Free"},"a"]},
 "FreeB":{"Fn::Equals":[{"Ref":"Free"},"b"]},
 "FreeA2":{"Fn::Equals":["a",{"Ref":"Free"}]},
 "ListA":{"Fn::Equals":[{"Ref":"List"},"a"]},
 "Same":{"Fn::Equals":[{"Ref":"Free"},{"Ref":"Free"}]},
 "Opaque":{"Fn::Equals":[{"Ref":"Free"},{"Ref":"Env"}]},
 "True":{"Fn::Equals":["1",1]},
 "False":{"Fn::Equals":[true,"false"]},
 "Loop":{"Fn::Not":[{"Condition":"Loop"}]},
 "Malformed":{"Fn::Equals":["only"]}}}`)
	cases := []struct {
		names []string
		given []Literal
		want  [][]bool
	}{
		{[]string{"Prod", "Dev"}, nil, [][]bool{{false, true}, {true, false}}},
		{[]string{"Prod", "NotProd"}, nil, [][]bool{{false, true}, {true, false}}},
		{[]string{"Both", "Either"}, nil, [][]bool{{false, true}}},
		{[]string{"Http", "HttpNumber"}, nil, [][]bool{{false, false}, {true, true}}},
		{[]string{"FreeA", "FreeB"}, nil, [][]bool{{false, false}, {false, true}, {true, false}}},
		{[]string{"FreeA", "FreeA2"}, nil, [][]bool{{false, false}, {true, true}}},
		{[]string{"ListA"}, nil, [][]bool{{false}, {true}}},
		{[]string{"Same", "True", "False"}, nil, [][]bool{{true, true, false}}},
		{[]string{"Opaque", "Prod"}, nil, [][]bool{{false, false}, {false, true}, {true, false}, {true, true}}},
		{[]string{"Prod"}, []Literal{{"Dev", true}}, [][]bool{{false}}},
		{[]string{"Prod"}, []Literal{{"False", true}}, nil},
		// Undeclared, cyclic and malformed conditions are unknown, and each
		// use of a name has the same value.
		{[]string{"Missing", "Loop", "Malformed"}, nil, [][]bool{
			{false, false, false}, {false, false, true}, {false, true, false}, {false, true, true},
			{true, false, false}, {true, false, true}, {true, true, false}, {true, true, true}}},
		{[]string{"Missing"}, []Literal{{"Missing", false}}, [][]bool{{false}}},
	}
	for _, test := range cases {
		got, ok := m.Combinations(test.names, test.given)
		if !ok || !reflect.DeepEqual(got, test.want) {
			t.Errorf("%v given %v: got %v (decided %t), want %v", test.names, test.given, got, ok, test.want)
		}
	}
	if !m.Declared("Prod") || m.Declared("Missing") {
		t.Error("Declared")
	}
}

func TestUndecided(t *testing.T) {
	conditions := map[string]any{}
	var names []string
	for i := 0; i < 15; i++ {
		name := fmt.Sprintf("C%d", i)
		conditions[name] = map[string]any{"Fn::Equals": []any{map[string]any{"Ref": fmt.Sprintf("P%d", i)}, "x"}}
		names = append(names, name)
	}
	m := New(map[string]any{"Conditions": conditions})
	if _, ok := m.Combinations(names, nil); ok {
		t.Fatal("15 independent comparisons exceed MaxAssignments")
	}
	if ok, decided := m.Satisfiable([]Literal{{"C0", true}, {"C1", false}}); !ok || !decided {
		t.Fatal("a small query is decided")
	}
}
