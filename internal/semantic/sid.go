package semantic

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// sidPlacements are the identity, trust and KMS key policy properties of
// internal/generate/iam.go, as property paths. "*" is a list index. As in
// cfn-lint E3510, S3 bucket policies and Organizations policies may repeat a
// Sid; TestSidPlacements keeps the two lists in step.
var sidPlacements = map[string][]string{
	"AWS::IAM::Role":          {"AssumeRolePolicyDocument", "Policies/*/PolicyDocument"},
	"AWS::IAM::User":          {"Policies/*/PolicyDocument"},
	"AWS::IAM::Group":         {"Policies/*/PolicyDocument"},
	"AWS::IAM::Policy":        {"PolicyDocument"},
	"AWS::IAM::ManagedPolicy": {"PolicyDocument"},
	"AWS::IAM::RolePolicy":    {"PolicyDocument"},
	"AWS::IAM::UserPolicy":    {"PolicyDocument"},
	"AWS::IAM::GroupPolicy":   {"PolicyDocument"},
	"AWS::SSO::PermissionSet": {"InlinePolicy"},
	"AWS::KMS::Key":           {"KeyPolicy"},
	"AWS::KMS::ReplicaKey":    {"KeyPolicy"},
}

// sidEntry is one Statement list entry that uses a Sid. Its Fn::If branches
// may use the Sid more than once; the entry is present when any of them is.
type sidEntry struct {
	paths  [][]string
	guards [][]Guard
}

// duplicateSids reports a literal Sid that another statement of the same
// policy also uses, unless the two statements' conditions never hold together.
func (c *checker) duplicateSids(template map[string]any) {
	resources, _ := template["Resources"].(map[string]any)
	for _, name := range sortedKeys(resources) {
		r, _ := resources[name].(map[string]any)
		typ, _ := r["Type"].(string)
		properties, _ := r["Properties"].(map[string]any)
		paths := sidPlacements[typ]
		var outer []Guard
		if condition, ok := r["Condition"].(string); ok {
			outer = append(outer, Guard{condition, true})
		}
		for _, p := range paths {
			c.documents(properties, strings.Split(p, "/"), []string{"Resources", name, "Properties"}, outer)
		}
	}
}

// branches calls f for each value v may take through Fn::If, skipping
// AWS::NoValue.
func branches(v any, path []string, guards []Guard, f func(v any, path []string, guards []Guard)) {
	if m, ok := v.(map[string]any); ok && len(m) == 1 {
		if args, ok := m["Fn::If"].([]any); ok && len(args) == 3 {
			if condition, ok := args[0].(string); ok {
				for i, value := range []bool{true, false} {
					g := append(guards[:len(guards):len(guards)], Guard{condition, value})
					branches(args[i+1], append(path[:len(path):len(path)], "Fn::If", strconv.Itoa(i+1)), g, f)
				}
				return
			}
		}
		if m["Ref"] == "AWS::NoValue" {
			return
		}
	}
	f(v, path, guards)
}

func (c *checker) documents(v any, segments, path []string, guards []Guard) {
	branches(v, path, guards, func(v any, path []string, guards []Guard) {
		if len(segments) == 0 {
			if s, ok := v.(string); ok {
				var decoded any
				if json.Unmarshal([]byte(s), &decoded) != nil {
					return
				}
				v = decoded
			}
			if doc, ok := v.(map[string]any); ok {
				c.statements(doc, path, guards)
			}
			return
		}
		switch v := v.(type) {
		case []any:
			if segments[0] == "*" {
				for i, x := range v {
					c.documents(x, segments[1:], append(path[:len(path):len(path)], strconv.Itoa(i)), guards)
				}
			}
		case map[string]any:
			if x, ok := v[segments[0]]; ok && segments[0] != "*" {
				c.documents(x, segments[1:], append(path[:len(path):len(path)], segments[0]), guards)
			}
		}
	})
}

func (c *checker) statements(doc map[string]any, path []string, outer []Guard) {
	var order []string
	// lists holds, per Sid, the entries of each Statement list that use it.
	lists := map[string][][]*sidEntry{}
	// A whole-Statement Fn::If chooses one list. Its guards are inner: the
	// policy exists in both branches.
	branches(doc["Statement"], append(path[:len(path):len(path)], "Statement"), nil, func(v any, path []string, list []Guard) {
		entries, _ := v.([]any)
		uses := map[string][]*sidEntry{}
		var listOrder []string
		for i, entry := range entries {
			seen := map[string]*sidEntry{}
			branches(entry, append(path[:len(path):len(path)], strconv.Itoa(i)), list, func(v any, path []string, guards []Guard) {
				s, _ := v.(map[string]any)
				sid, ok := s["Sid"].(string)
				if !ok {
					return
				}
				e := seen[sid]
				if e == nil {
					if _, ok := uses[sid]; !ok {
						listOrder = append(listOrder, sid)
					}
					e = &sidEntry{}
					seen[sid] = e
					uses[sid] = append(uses[sid], e)
				}
				e.paths = append(e.paths, append(path[:len(path):len(path)], "Sid"))
				e.guards = append(e.guards, guards)
			})
		}
		for _, sid := range listOrder {
			if len(uses[sid]) < 2 {
				continue
			}
			if _, ok := lists[sid]; !ok {
				order = append(order, sid)
			}
			lists[sid] = append(lists[sid], uses[sid])
		}
	})
	s := sidPolicy{c: c, outer: outer, policy: c.m.Conditions.guards(outer), truth: map[string]Truth{}}
	for _, sid := range order {
		s.check(sid, lists[sid])
	}
}

// sidPolicy checks the Sids of one policy, which exists when outer holds.
type sidPolicy struct {
	c      *checker
	outer  []Guard
	policy *formula
	// truth caches Classify by guard set; entries often share conditions.
	truth map[string]Truth
}

// present is the formula for an entry existing within its policy.
func (s *sidPolicy) present(e *sidEntry) *formula {
	fs := make([]*formula, len(e.guards))
	for i, g := range e.guards {
		fs[i] = s.c.m.Conditions.guards(g)
	}
	return or(fs...)
}

func (s *sidPolicy) classify(guards []Guard) Truth {
	guards = dedupeGuards(append(guards[:len(guards):len(guards)], s.outer...))
	keys := make([]string, len(guards))
	for i, g := range guards {
		keys[i] = g.Condition + "=" + strconv.FormatBool(g.Value)
	}
	sort.Strings(keys)
	key := strings.Join(keys, "\x00")
	t, ok := s.truth[key]
	if !ok {
		t = s.c.m.Conditions.Classify(guards)
		s.truth[key] = t
	}
	return t
}

// check reports each entry that repeats the Sid of an earlier entry in its
// list. It is an error when the policy existing implies the entry and an
// earlier one exist, and a warning naming the conditions of one pair of
// branches when they only can. If no entry is certain but the policy implies
// some list repeats the Sid, as with a whole-Statement Fn::If whose lists
// both repeat it, every report is an error.
func (s *sidPolicy) check(sid string, lists [][]*sidEntry) {
	conds := s.c.m.Conditions
	type repeat struct {
		earlier []*sidEntry
		b       *sidEntry
		certain bool
	}
	var found []repeat
	var possible []*formula
	anyCertain := false
	for _, entries := range lists {
		earlier := s.present(entries[0])
		for j := 1; j < len(entries); j++ {
			p := s.present(entries[j])
			both := and(p, earlier)
			earlier = or(earlier, p)
			if conds.classify(and(s.policy, both)) == Never {
				continue
			}
			certain := conds.classify(and(s.policy, not(both))) == Never
			anyCertain = anyCertain || certain
			found = append(found, repeat{entries[:j], entries[j], certain})
			possible = append(possible, both)
		}
	}
	if len(found) == 0 {
		return
	}
	all := !anyCertain && conds.classify(and(s.policy, not(or(possible...)))) == Never
	for _, r := range found {
		s.report(sid, r.earlier, r.b, r.certain || all)
	}
}

// report names the first pair of branches of b and an earlier entry that can
// exist together.
func (s *sidPolicy) report(sid string, earlier []*sidEntry, b *sidEntry, certain bool) {
	for k, bg := range b.guards {
		for _, a := range earlier {
			for l, ag := range a.guards {
				guards := append(ag[:len(ag):len(ag)], bg...)
				if s.classify(guards) == Never {
					continue
				}
				if certain {
					s.c.report("duplicate-sid", Error, b.paths[k], nil, "Sid %q is also used by %s", sid, Pointer(a.paths[l]))
				} else {
					s.c.report("duplicate-sid", Warning, b.paths[k], dedupeGuards(guards),
						"Sid %q is also used by %s when these conditions hold", sid, Pointer(a.paths[l]))
				}
				return
			}
		}
	}
}

func dedupeGuards(guards []Guard) []Guard {
	var out []Guard
	seen := map[Guard]bool{}
	for _, g := range guards {
		if !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	return out
}
