package generate

import (
	"errors"
	"fmt"
	"regexp/syntax"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// This file rewrites lookaround shapes that decomposeAlternative cannot
// place at a fixed offset. Each rewrite replaces part of the pattern with an
// equivalent one, so the result is exact. It runs only for alternatives the
// positional translation rejects.
//
// A lookaround whose body only tests one character, such as (?!-), (?!\d+)
// or (?<!-), is folded into the atom next to it: (?!-)[a-z-]{1,62} becomes
// [a-z][a-z-]{0,61}. That holds wherever the pair occurs, including inside
// a repeated group, because the body only reads a character the atom
// consumes.

// eliminateCharLooks folds every one-character lookaround in nodes into the
// atom it tests. It fails when one cannot be folded, for example when no
// atom inside its group follows a lookahead.
func eliminateCharLooks(nodes []reNode) ([]reNode, error) {
	budget := maxExpandedAlternatives
	return (&charFolder{budget: &budget}).sequence(nodes)
}

type charFolder struct {
	budget *int
}

type charLook struct {
	set    []rune // sorted rune ranges, as in syntax.Regexp.Rune
	keep   bool   // a positive lookaround: the character must be in set
	behind bool
}

func (l charLook) restrict(class []rune) []rune {
	if l.keep {
		return intersectRanges(class, l.set)
	}
	return intersectRanges(class, complementRanges(l.set))
}

func (f *charFolder) spend() error {
	*f.budget--
	if *f.budget < 0 {
		return errors.New("too many alternatives around lookarounds")
	}
	return nil
}

func (f *charFolder) sequence(nodes []reNode) ([]reNode, error) {
	nodes = append([]reNode(nil), nodes...)
	for i, n := range nodes {
		if n.kind == nodeFlags {
			return nil, errors.New("inline flags")
		}
		if n.kind != nodeGroup || !containsLookaround([]reNode{n}) {
			continue
		}
		if !plainGroup(n.open) {
			return nil, errors.New("lookaround inside a group with inline flags")
		}
		alts := make([][]reNode, len(n.alts))
		for j, alt := range n.alts {
			a, err := f.sequence(alt)
			if err != nil {
				return nil, err
			}
			alts[j] = a
		}
		nodes[i].alts = alts
	}
	for i, n := range nodes {
		if n.kind != nodeLook {
			continue
		}
		l, ok := oneCharLook(n)
		if !ok {
			continue
		}
		var out []reNode
		if l.behind {
			head, err := f.behind(l, nodes[:i])
			if err != nil {
				return nil, err
			}
			out = append(head, nodes[i+1:]...)
		} else {
			tail, err := f.ahead(l, nodes[i+1:])
			if err != nil {
				return nil, err
			}
			out = append(append([]reNode(nil), nodes[:i]...), tail...)
		}
		// Folding can copy lookarounds into new groups; fold those too.
		return f.sequence(out)
	}
	return nodes, nil
}

var never = reNode{kind: nodeText, text: renderClass(nil)}

// ahead folds a lookahead into the first atom of rest.
func (f *charFolder) ahead(l charLook, rest []reNode) ([]reNode, error) {
	if len(rest) == 0 {
		return nil, errors.New("lookahead at the end of a group")
	}
	n, after := rest[0], rest[1:]
	switch {
	case n.kind == nodeBegin || n.kind == nodeLook:
		// Assertions at the same position commute.
		tail, err := f.ahead(l, after)
		return append([]reNode{n}, tail...), err
	case n.kind == nodeText && n.text == "$" && n.quant == "":
		// No character follows the end of the string.
		if l.keep {
			return []reNode{never}, nil
		}
		return rest, nil
	case n.kind == nodeGroup && n.quant == "" && plainGroup(n.open):
		if len(n.alts) == 1 {
			return f.ahead(l, join(n.alts[0], after))
		}
		alts := make([][]reNode, len(n.alts))
		for i, alt := range n.alts {
			if i > 0 {
				if err := f.spend(); err != nil {
					return nil, err
				}
			}
			a, err := f.ahead(l, join(alt, after))
			if err != nil {
				return nil, err
			}
			alts[i] = a
		}
		return []reNode{{kind: nodeGroup, open: "(?:", alts: alts}}, nil
	}
	class, atom, lo, hi, ok := charAtom(n)
	if !ok {
		return nil, fmt.Errorf("lookahead before %q", renderNodes([]reNode{n}))
	}
	first := reNode{kind: nodeText, text: renderClass(l.restrict(class))}
	take := join(append([]reNode{first}, repeatNode(atom, max(lo-1, 0), decrement(hi))...), after)
	if lo >= 1 {
		return take, nil
	}
	if err := f.spend(); err != nil {
		return nil, err
	}
	skip, err := f.ahead(l, after)
	if err != nil {
		return nil, err
	}
	return []reNode{{kind: nodeGroup, open: "(?:", alts: [][]reNode{take, skip}}}, nil
}

// behind folds a lookbehind into the last atom of before.
func (f *charFolder) behind(l charLook, before []reNode) ([]reNode, error) {
	if len(before) == 0 {
		return nil, errors.New("lookbehind at the start of a group")
	}
	n, prefix := before[len(before)-1], before[:len(before)-1]
	switch {
	case n.kind == nodeBegin:
		// No character precedes the start of the string.
		if l.keep {
			return []reNode{never}, nil
		}
		return before, nil
	case n.kind == nodeLook:
		head, err := f.behind(l, prefix)
		return append(head, n), err
	case n.kind == nodeText && n.text == "$" && n.quant == "":
		head, err := f.behind(l, prefix)
		return append(head, n), err
	case n.kind == nodeGroup && n.quant == "" && plainGroup(n.open):
		if len(n.alts) == 1 {
			return f.behind(l, join(prefix, n.alts[0]))
		}
		alts := make([][]reNode, len(n.alts))
		for i, alt := range n.alts {
			if i > 0 {
				if err := f.spend(); err != nil {
					return nil, err
				}
			}
			a, err := f.behind(l, join(prefix, alt))
			if err != nil {
				return nil, err
			}
			alts[i] = a
		}
		return []reNode{{kind: nodeGroup, open: "(?:", alts: alts}}, nil
	}
	class, atom, lo, hi, ok := charAtom(n)
	if !ok {
		return nil, fmt.Errorf("lookbehind after %q", renderNodes([]reNode{n}))
	}
	last := reNode{kind: nodeText, text: renderClass(l.restrict(class))}
	take := append(join(prefix, repeatNode(atom, max(lo-1, 0), decrement(hi))), last)
	if lo >= 1 {
		return take, nil
	}
	if err := f.spend(); err != nil {
		return nil, err
	}
	skip, err := f.behind(l, prefix)
	if err != nil {
		return nil, err
	}
	return []reNode{{kind: nodeGroup, open: "(?:", alts: [][]reNode{take, skip}}}, nil
}

func join(a, b []reNode) []reNode {
	return append(append([]reNode(nil), a...), b...)
}

func decrement(n int) int {
	if n < 0 {
		return n
	}
	return n - 1
}

func plainGroup(open string) bool {
	return open == "(" || open == "(?:" || namedGroup.MatchString(open)
}

// oneCharLook reports whether a lookaround only tests the character next to
// its position, and which characters pass. (?!\d+) is (?!\d): \d+ matches
// wherever \d does.
func oneCharLook(n reNode) (charLook, bool) {
	re, err := syntax.Parse(re2Pattern(n.body), syntax.Perl)
	if err != nil {
		return charLook{}, false
	}
	behind := strings.HasPrefix(n.look, "<")
	set, ok := testedChar(re, behind)
	if !ok {
		return charLook{}, false
	}
	return charLook{set: set, keep: n.look == "=" || n.look == "<=", behind: behind}, true
}

// testedChar returns the characters re can start with (or end with, behind)
// when whether re matches at a position depends only on that character.
func testedChar(re *syntax.Regexp, behind bool) ([]rune, bool) {
	if class, ok := oneChar(re); ok {
		return class, true
	}
	switch re.Op {
	case syntax.OpCapture, syntax.OpPlus:
		return testedChar(re.Sub[0], behind)
	case syntax.OpRepeat:
		// Only a{1,n} matches wherever a does; a{2} needs a second character.
		if re.Min == 1 {
			return testedChar(re.Sub[0], behind)
		}
	case syntax.OpConcat:
		edge, others := re.Sub[0], re.Sub[1:]
		if behind {
			edge, others = re.Sub[len(re.Sub)-1], re.Sub[:len(re.Sub)-1]
		}
		for _, sub := range others {
			if !matchesEmptyAnywhere(sub) {
				return nil, false
			}
		}
		return testedChar(edge, behind)
	case syntax.OpAlternate:
		var union []rune
		for _, sub := range re.Sub {
			class, ok := testedChar(sub, behind)
			if !ok {
				return nil, false
			}
			union = append(union, class...)
		}
		return normalizeRanges(union), true
	}
	return nil, false
}

// oneChar returns the characters re matches when it matches exactly one.
func oneChar(re *syntax.Regexp) ([]rune, bool) {
	switch re.Op {
	case syntax.OpCharClass:
		return normalizeRanges(append([]rune(nil), re.Rune...)), true
	case syntax.OpLiteral:
		if len(re.Rune) == 1 && re.Flags&syntax.FoldCase == 0 {
			return []rune{re.Rune[0], re.Rune[0]}, true
		}
	case syntax.OpAnyCharNotNL:
		return []rune{0, '\n' - 1, '\n' + 1, unicode.MaxRune}, true
	case syntax.OpAnyChar:
		return []rune{0, unicode.MaxRune}, true
	case syntax.OpCapture:
		return oneChar(re.Sub[0])
	}
	return nil, false
}

// matchesEmptyAnywhere reports whether re matches the empty string at every
// position, so it never decides whether a concatenation matches.
func matchesEmptyAnywhere(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpEmptyMatch, syntax.OpStar, syntax.OpQuest:
		return assertionFree(re)
	case syntax.OpRepeat:
		return assertionFree(re) && (re.Min == 0 || matchesEmptyAnywhere(re.Sub[0]))
	case syntax.OpCapture, syntax.OpPlus:
		return matchesEmptyAnywhere(re.Sub[0])
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			if !matchesEmptyAnywhere(sub) {
				return false
			}
		}
		return true
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if matchesEmptyAnywhere(sub) {
				return true
			}
		}
	}
	return false
}

func assertionFree(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary, syntax.OpNoMatch:
		return false
	}
	for _, sub := range re.Sub {
		if !assertionFree(sub) {
			return false
		}
	}
	return true
}

// charAtom reports whether n consumes only characters of one class, as
// [a-z]{0,62} or ([a-z]) do, and returns the class, the unquantified atom
// and the repeat bounds; max is -1 when unbounded.
func charAtom(n reNode) (class []rune, atom reNode, min, max int, ok bool) {
	atom = n
	atom.quant = ""
	switch {
	case n.kind == nodeText:
	case n.kind == nodeGroup && plainGroup(n.open) && len(n.alts) == 1 && len(n.alts[0]) == 1:
		inner := n.alts[0][0]
		if inner.quant != "" || inner.kind != nodeText {
			return nil, reNode{}, 0, 0, false
		}
		atom = inner
	default:
		return nil, reNode{}, 0, 0, false
	}
	re, err := syntax.Parse(re2Pattern(atom.text), syntax.Perl)
	if err != nil {
		return nil, reNode{}, 0, 0, false
	}
	if class, ok = oneChar(re); !ok {
		return nil, reNode{}, 0, 0, false
	}
	min, max, ok = quantBounds(n.quant)
	return class, atom, min, max, ok
}

func quantBounds(q string) (min, max int, ok bool) {
	if q == "" {
		return 1, 1, true
	}
	base := q[:1]
	if q[0] == '{' {
		base = q[:strings.IndexByte(q, '}')+1]
	}
	if suffix := q[len(base):]; suffix != "" && suffix != "?" {
		return 0, 0, false // possessive
	}
	re, err := syntax.Parse("a"+base, syntax.Perl)
	if err != nil {
		return 0, 0, false
	}
	switch re.Op {
	case syntax.OpStar:
		return 0, -1, true
	case syntax.OpPlus:
		return 1, -1, true
	case syntax.OpQuest:
		return 0, 1, true
	case syntax.OpRepeat:
		return re.Min, re.Max, true
	}
	return 0, 0, false
}

// repeatNode renders atom repeated between min and max times (max -1 for
// unbounded), or nothing when max is 0.
func repeatNode(atom reNode, min, max int) []reNode {
	var q string
	switch {
	case max == 0:
		return nil
	case max < 0 && min == 0:
		q = "*"
	case max < 0 && min == 1:
		q = "+"
	case max < 0:
		q = "{" + strconv.Itoa(min) + ",}"
	case min == max && min == 1:
	case min == max:
		q = "{" + strconv.Itoa(min) + "}"
	case min == 0 && max == 1:
		q = "?"
	default:
		q = "{" + strconv.Itoa(min) + "," + strconv.Itoa(max) + "}"
	}
	atom.quant = q
	return []reNode{atom}
}

func normalizeRanges(r []rune) []rune {
	type span struct{ lo, hi rune }
	spans := make([]span, 0, len(r)/2)
	for i := 0; i+1 < len(r); i += 2 {
		spans = append(spans, span{r[i], r[i+1]})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].lo < spans[j].lo })
	var out []rune
	for _, s := range spans {
		if n := len(out); n > 0 && s.lo <= out[n-1]+1 {
			if s.hi > out[n-1] {
				out[n-1] = s.hi
			}
			continue
		}
		out = append(out, s.lo, s.hi)
	}
	return out
}

func complementRanges(r []rune) []rune {
	var out []rune
	next := rune(0)
	for i := 0; i+1 < len(r); i += 2 {
		if r[i] > next {
			out = append(out, next, r[i]-1)
		}
		next = r[i+1] + 1
	}
	if next <= unicode.MaxRune {
		out = append(out, next, unicode.MaxRune)
	}
	return out
}

func intersectRanges(a, b []rune) []rune {
	var out []rune
	for i, j := 0, 0; i+1 < len(a) && j+1 < len(b); {
		lo, hi := max(a[i], b[j]), min(a[i+1], b[j+1])
		if lo <= hi {
			out = append(out, lo, hi)
		}
		if a[i+1] < b[j+1] {
			i += 2
		} else {
			j += 2
		}
	}
	return out
}

// renderClass writes rune ranges as an RE2 character class. An empty class
// matches nothing.
func renderClass(r []rune) string {
	if len(r) == 0 {
		return `[^\x00-\x{10ffff}]`
	}
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i+1 < len(r); i += 2 {
		b.WriteString(classRune(r[i]))
		if r[i+1] > r[i]+1 {
			b.WriteByte('-')
		}
		if r[i+1] > r[i] {
			b.WriteString(classRune(r[i+1]))
		}
	}
	b.WriteByte(']')
	return b.String()
}

func classRune(r rune) string {
	switch {
	case r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
		return string(r)
	case r > ' ' && r < 0x7f:
		return `\` + string(r)
	}
	return `\x{` + strconv.FormatInt(int64(r), 16) + `}`
}

// rewriteAlternative translates an alternative after one-character
// lookarounds are folded.
func rewriteAlternative(nodes []reNode) (branches []any, supersets []string, err error) {
	if branch, superset, ok := scanAlternative(nodes); ok {
		return []any{branch}, []string{superset}, nil
	}
	expanded, err := expandGroups(nodes)
	if err != nil {
		return nil, nil, err
	}
	for _, seq := range expanded {
		options := [][]reNode{seq}
		if _, _, err := decomposeAlternative(seq); err != nil {
			if options, err = expandOptional(seq, maxExpandedAlternatives); err != nil {
				return nil, nil, err
			}
		}
		for _, option := range options {
			branch, superset, err := decomposeAlternative(option)
			if err != nil {
				return nil, nil, err
			}
			if branch != nil {
				branches = append(branches, branch)
				supersets = append(supersets, superset)
			}
		}
	}
	return branches, supersets, nil
}

// expandOptional distributes optional atoms and groups before the last
// lookaround, so ^(-?)P(?=T) becomes ^P(?=T) and ^-P(?=T), each with the
// lookahead at a fixed offset.
func expandOptional(seq []reNode, limit int) ([][]reNode, error) {
	last := -1
	for i, n := range seq {
		if n.kind == nodeLook {
			last = i
		}
	}
	for j := 0; j < last; j++ {
		n := seq[j]
		if n.kind != nodeText && n.kind != nodeGroup {
			continue
		}
		re, err := syntax.Parse(re2Pattern(renderNodes([]reNode{n})), syntax.Perl)
		if err != nil {
			return nil, err
		}
		if min, max := width(re); min == max && max >= 0 {
			continue
		}
		var options [][]reNode
		switch {
		case n.quant == "?" || n.quant == "??":
			once := n
			once.quant = ""
			options = [][]reNode{nil, {once}}
		case n.kind == nodeGroup && n.quant == "" && plainGroup(n.open):
			options = n.alts
		default:
			return [][]reNode{seq}, nil
		}
		var out [][]reNode
		for _, option := range options {
			more, err := expandOptional(join(join(seq[:j], option), seq[j+1:]), limit)
			if err != nil {
				return nil, err
			}
			out = append(out, more...)
			if len(out) > limit {
				return nil, errors.New("too many alternatives around lookarounds")
			}
		}
		return out, nil
	}
	return [][]reNode{seq}, nil
}

// scanAlternative translates ^(?:(?!B)C)*$, where C matches one character:
// every position of the string starts an iteration, so B must not match
// anywhere. B must consume a character, or it would also be tested at the
// end of the string, where no iteration starts.
func scanAlternative(nodes []reNode) (map[string]any, string, bool) {
	if len(nodes) != 3 || nodes[0].kind != nodeBegin || nodes[2].kind != nodeText || nodes[2].text != "$" || nodes[2].quant != "" {
		return nil, "", false
	}
	g := nodes[1]
	if g.kind != nodeGroup || !plainGroup(g.open) || g.quant == "" || len(g.alts) != 1 || len(g.alts[0]) != 2 {
		return nil, "", false
	}
	look, char := g.alts[0][0], g.alts[0][1]
	if look.kind != nodeLook || look.look != "!" {
		return nil, "", false
	}
	if _, _, min, max, ok := charAtom(char); !ok || min != 1 || max != 1 {
		return nil, "", false
	}
	if _, _, ok := quantBounds(g.quant); !ok {
		return nil, "", false
	}
	body := re2Pattern(look.body)
	re, err := syntax.Parse(body, syntax.Perl)
	if err != nil {
		return nil, "", false
	}
	if min, _ := width(re); min < 1 {
		return nil, "", false
	}
	rest := re2Pattern(renderNodes([]reNode{{kind: nodeBegin}, {kind: nodeGroup, open: "(?:", alts: [][]reNode{{char}}, quant: g.quant}, nodes[2]}))
	schema := map[string]any{"allOf": []any{
		map[string]any{"pattern": rest},
		map[string]any{"not": map[string]any{"type": "string", "pattern": body}},
	}}
	if compileCondition(schema) != nil {
		return nil, "", false
	}
	return schema, rest, true
}
