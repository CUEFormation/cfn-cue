package semantic

import (
	"sort"
	"strings"
)

// maxCycles and maxSteps bound simple-cycle enumeration in one strongly
// connected component whose edges all depend on conditions.
const (
	maxCycles = 256
	maxSteps  = 100000
)

// edge holds the alternative guard sets under which one resource refers to
// another. The edge exists when any alternative holds.
type edge struct{ alternatives [][]Guard }

// cycles reports resource dependency cycles from Ref, Fn::GetAtt, Fn::Sub and
// DependsOn. CloudFormation resolves conditions before ordering resources, so
// a reference inside an untaken Fn::If branch, or from a resource whose
// Condition is false, adds no dependency. A cycle present for every condition
// value is an error. A cycle that depends on unresolved conditions is a
// warning, and one whose conditions cannot hold together is not reported.
func (c *checker) cycles() {
	graph := map[string]map[string]*edge{}
	for _, r := range c.m.References {
		if r.Section != "Resources" {
			continue
		}
		switch r.Kind {
		case KindRef, KindGetAtt, KindSub, KindDependsOn:
		default:
			continue
		}
		if _, ok := c.m.Resources[r.Target]; !ok {
			continue
		}
		if graph[r.Owner] == nil {
			graph[r.Owner] = map[string]*edge{}
		}
		e := graph[r.Owner][r.Target]
		if e == nil {
			e = &edge{}
			graph[r.Owner][r.Target] = e
		}
		e.alternatives = append(e.alternatives, r.Guards)
	}
	// Classify each edge once. Unconditional edges form the error graph.
	certain := map[string][]string{}
	possible := map[string][]string{}
	for _, from := range sortedKeys(graph) {
		for _, to := range sortedKeys(graph[from]) {
			t := c.m.Conditions.classify(c.edgeFormula(graph[from][to]))
			if t == Never {
				continue
			}
			possible[from] = append(possible[from], to)
			if t == Always {
				certain[from] = append(certain[from], to)
			}
		}
	}
	reported := map[string]bool{}
	for _, component := range components(certain) {
		cycle := findCycle(certain, component)
		for _, name := range component {
			reported[name] = true
		}
		c.report("dependency-cycle", Error, []string{"Resources", cycle[0]}, nil,
			"circular dependency: %s", strings.Join(cycle, " -> "))
	}
	for _, component := range components(possible) {
		if reported[component[0]] {
			continue
		}
		members := map[string]bool{}
		for _, name := range component {
			members[name] = true
		}
		var found []string
		var foundTruth Truth
		count := 0
		complete := simpleCycles(possible, component, members, func(cycle []string) bool {
			count++
			edges := make([]*formula, 0, len(cycle)-1)
			for i := 0; i+1 < len(cycle); i++ {
				edges = append(edges, c.edgeFormula(graph[cycle[i]][cycle[i+1]]))
			}
			if t := c.m.Conditions.classify(and(edges...)); t != Never {
				found, foundTruth = cycle, t
				return false
			}
			return count < maxCycles
		})
		switch {
		case found != nil && foundTruth == Always:
			// Each edge depends on conditions, but together they always hold.
			c.report("dependency-cycle", Error, []string{"Resources", found[0]}, nil,
				"circular dependency: %s", strings.Join(found, " -> "))
		case found != nil:
			c.report("dependency-cycle", Warning, []string{"Resources", found[0]}, c.cycleGuards(graph, found),
				"circular dependency when its conditions hold: %s", strings.Join(found, " -> "))
		case !complete:
			c.report("dependency-cycle", Warning, []string{"Resources", component[0]}, nil,
				"conditional dependencies among %s were not fully analyzed; a cycle cannot be ruled out", strings.Join(component, ", "))
		}
	}
}

func (c *checker) edgeFormula(e *edge) *formula {
	fs := make([]*formula, len(e.alternatives))
	for i, guards := range e.alternatives {
		fs[i] = c.m.Conditions.guards(guards)
	}
	return or(fs...)
}

// cycleGuards lists the guards of a cycle's edges when each edge has one
// alternative, which identifies the conditions to inspect.
func (c *checker) cycleGuards(graph map[string]map[string]*edge, cycle []string) []Guard {
	seen := map[Guard]bool{}
	var out []Guard
	for i := 0; i+1 < len(cycle); i++ {
		e := graph[cycle[i]][cycle[i+1]]
		if len(e.alternatives) != 1 {
			return nil
		}
		for _, g := range e.alternatives[0] {
			if !seen[g] {
				seen[g] = true
				out = append(out, g)
			}
		}
	}
	return out
}

// components returns the strongly connected components that contain a cycle,
// each sorted, in order of their first member (Tarjan's algorithm).
func components(graph map[string][]string) [][]string {
	index := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var out [][]string
	var visit func(string)
	visit = func(v string) {
		index[v] = len(index)
		low[v] = index[v]
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range graph[v] {
			if _, ok := index[w]; !ok {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] != index[v] {
			return
		}
		var component []string
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false
			component = append(component, w)
			if w == v {
				break
			}
		}
		if len(component) > 1 || contains(graph[v], v) {
			sort.Strings(component)
			out = append(out, component)
		}
	}
	for _, v := range sortedKeys(graph) {
		if _, ok := index[v]; !ok {
			visit(v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// findCycle returns a shortest cycle through the component's first member,
// ending where it starts.
func findCycle(graph map[string][]string, component []string) []string {
	start := component[0]
	parent := map[string]string{}
	queue := []string{start}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, w := range graph[v] {
			if w == start {
				cycle := []string{start}
				for x := v; x != start; x = parent[x] {
					cycle = append(cycle, x)
				}
				for i, j := 1, len(cycle)-1; i < j; i, j = i+1, j-1 {
					cycle[i], cycle[j] = cycle[j], cycle[i]
				}
				return append(cycle, start)
			}
			if _, seen := parent[w]; !seen && w != start {
				parent[w] = v
				queue = append(queue, w)
			}
		}
	}
	return []string{start, start}
}

// simpleCycles calls yield for each simple cycle in members that starts at a
// listed start node and otherwise visits only later members, so each cycle is
// produced once. It stops when yield returns false or the step budget runs
// out, and reports whether the enumeration completed.
func simpleCycles(graph map[string][]string, starts []string, members map[string]bool, yield func([]string) bool) bool {
	order := sortedKeys(members)
	rank := map[string]int{}
	for i, name := range order {
		rank[name] = i
	}
	steps := 0
	for _, start := range starts {
		path := []string{start}
		onPath := map[string]bool{start: true}
		var dfs func(string) bool
		dfs = func(v string) bool {
			for _, w := range graph[v] {
				if steps++; steps > maxSteps {
					return false
				}
				if !members[w] || rank[w] < rank[start] {
					continue
				}
				if w == start {
					cycle := append(append([]string{}, path...), start)
					if !yield(cycle) {
						return false
					}
					continue
				}
				if onPath[w] {
					continue
				}
				path = append(path, w)
				onPath[w] = true
				ok := dfs(w)
				path = path[:len(path)-1]
				onPath[w] = false
				if !ok {
					return false
				}
			}
			return true
		}
		if !dfs(start) {
			return false
		}
	}
	return true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
