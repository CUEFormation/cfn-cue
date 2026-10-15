package generate

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Provider patterns are ECMAScript regular expressions, which JSON Schema
// applies as an unanchored search. RE2, and so CUE, has no lookarounds.
// decomposeLookarounds rewrites a pattern with lookarounds into an equivalent
// schema of RE2 patterns: a positive pattern for what the match consumes,
// joined with a pattern for each lookahead that must match and a negated
// pattern for each that must not.
//
// The rewrite is exact only where every lookaround is evaluated at a position
// the string determines. That holds when the match must start at the
// beginning of the string and every lookaround follows a fixed-width prefix,
// as in ^(?!aws:)[a-z:]+$, where (?!aws:) is checked at offset 0 and becomes
// a negated ^(?:aws:). A lookbehind directly before $ is checked at the end of
// the string. Other placements return an error, and the caller keeps reporting
// the pattern instead of approximating it.
//
// The pieces keep the source's RE2 interpretation of escapes and classes, as
// for patterns RE2 compiles directly; \s, for example, is ASCII whitespace.
//
// The returned superset is an RE2 pattern that every matching string also
// matches, such as ^[a-z:]+$ for the example above.
func decomposeLookarounds(pattern string) (schema map[string]any, superset string, err error) {
	if !hasLookaround(pattern) {
		return nil, "", errNoLookaround
	}
	p := &reParser{src: pattern}
	alternatives, err := p.alternatives()
	if err != nil {
		return nil, "", err
	}
	if p.pos != len(p.src) {
		return nil, "", fmt.Errorf("unbalanced ) at offset %d", p.pos)
	}
	var plain, supersets []string
	var branches []any
	flags := false
	for _, alternative := range alternatives {
		for _, n := range alternative {
			// An inline flag also covers later alternatives, which are
			// translated separately.
			flags = flags || n.kind == nodeFlags
		}
		if flags && containsLookaround(alternative) {
			return nil, "", errors.New("inline flags outside a lookaround")
		}
		if !containsLookaround(alternative) {
			plain = append(plain, renderNodes(alternative))
			continue
		}
		expanded, err := expandGroups(alternative)
		if err != nil {
			return nil, "", err
		}
		for _, nodes := range expanded {
			branch, superset, err := decomposeAlternative(nodes)
			if err != nil {
				return nil, "", err
			}
			if branch != nil {
				branches = append(branches, branch)
				supersets = append(supersets, superset)
			}
		}
	}
	if len(plain) > 0 {
		joined := re2Pattern(strings.Join(plain, "|"))
		if _, err := regexp.Compile(joined); err != nil {
			return nil, "", fmt.Errorf("alternative without lookaround is incompatible with RE2: %w", err)
		}
		branches = append([]any{map[string]any{"pattern": joined}}, branches...)
		supersets = append([]string{joined}, supersets...)
	}
	switch len(branches) {
	case 0:
		return nil, "", errors.New("no alternative can match")
	case 1:
		return branches[0].(map[string]any), supersets[0], nil
	}
	return map[string]any{"anyOf": branches}, "(?:" + strings.Join(supersets, ")|(?:") + ")", nil
}

var errNoLookaround = errors.New("no lookaround")

// lookaroundDetail explains why a pattern with lookarounds was not
// translated, for a diagnostic.
func lookaroundDetail(err error) string {
	if errors.Is(err, errNoLookaround) {
		return ""
	}
	return "; lookarounds not translated: " + err.Error()
}

// lookaroundKeys translates a patternProperties key with lookarounds that
// only exclude names, such as ^(?!aws:)[a-z:]+$, into RE2 key patterns. The
// value schema applies to keys matching the wider pattern, here ^[a-z:]+$,
// and each excluded form, here ^(?:aws:), becomes a key pattern whose value
// must be absent. That is exact only when no other pattern, property or
// additionalProperties schema could admit an excluded key.
func lookaroundKeys(object map[string]any, key string) (string, []string, error) {
	schema, superset, err := decomposeLookarounds(key)
	if err != nil {
		return "", nil, err
	}
	patterns, _ := object["patternProperties"].(map[string]any)
	if properties, _ := object["properties"].(map[string]any); len(patterns) != 1 || len(properties) != 0 || object["additionalProperties"] != false {
		return "", nil, errors.New("key pattern must be the object's only way to name a property")
	}
	conditions := []any{schema}
	if all, ok := schema["allOf"].([]any); ok && len(schema) == 1 {
		conditions = all
	}
	var excluded []string
	for _, condition := range conditions {
		c, _ := condition.(map[string]any)
		if p, ok := c["pattern"].(string); ok && len(c) == 1 && p == superset {
			continue
		}
		n, _ := c["not"].(map[string]any)
		if p, ok := n["pattern"].(string); ok && len(c) == 1 && len(n) == 2 && n["type"] == "string" {
			excluded = append(excluded, p)
			continue
		}
		return "", nil, errors.New("key pattern requires more than excluded prefixes")
	}
	return superset, excluded, nil
}

func hasLookaround(pattern string) bool {
	for _, open := range []string{"(?=", "(?!", "(?<=", "(?<!"} {
		if strings.Contains(pattern, open) {
			return true
		}
	}
	return false
}

// maxExpandedAlternatives bounds distributing a group's alternatives over the
// concatenation around it.
const maxExpandedAlternatives = 32

type nodeKind int

const (
	nodeText  nodeKind = iota // an atom RE2 may compile, quantifier included
	nodeBegin                 // ^ or \A
	nodeFlags                 // an inline flag setter such as (?i)
	nodeLook                  // (?=X), (?!X), (?<=X) or (?<!X)
	nodeGroup                 // a group with its alternatives parsed
)

type reNode struct {
	kind  nodeKind
	text  string // nodeText and nodeFlags source
	look  string // "=", "!", "<=" or "<!"
	body  string // lookaround body source
	open  string // group opener, such as "(" or "(?:"
	alts  [][]reNode
	quant string
}

type reParser struct {
	src string
	pos int
}

func (p *reParser) alternatives() ([][]reNode, error) {
	var out [][]reNode
	var current []reNode
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case '|':
			p.pos++
			out = append(out, current)
			current = nil
			continue
		case ')':
			return append(out, current), nil
		}
		node, err := p.atom()
		if err != nil {
			return nil, err
		}
		if q := p.quantifier(); q != "" {
			if node.kind != nodeText && node.kind != nodeGroup {
				return nil, fmt.Errorf("quantified assertion %q", renderNodes([]reNode{node})+q)
			}
			node.quant = q
		}
		current = append(current, node)
	}
	return append(out, current), nil
}

func (p *reParser) atom() (reNode, error) {
	start := p.pos
	switch c := p.src[p.pos]; c {
	case '^':
		p.pos++
		return reNode{kind: nodeBegin}, nil
	case '\\':
		if strings.HasPrefix(p.src[p.pos:], `\A`) {
			p.pos += 2
			return reNode{kind: nodeBegin}, nil
		}
		if err := p.escape(); err != nil {
			return reNode{}, err
		}
		return reNode{kind: nodeText, text: p.src[start:p.pos]}, nil
	case '[':
		if err := p.class(); err != nil {
			return reNode{}, err
		}
		return reNode{kind: nodeText, text: p.src[start:p.pos]}, nil
	case '(':
		return p.group()
	default:
		_, size := utf8.DecodeRuneInString(p.src[p.pos:])
		p.pos += size
		return reNode{kind: nodeText, text: p.src[start:p.pos]}, nil
	}
}

var (
	inlineFlags = regexp.MustCompile(`^\(\?[a-zA-Z]*(?:-[a-zA-Z]*)?\)`)
	flagGroup   = regexp.MustCompile(`^\(\?[a-zA-Z]*(?:-[a-zA-Z]*)?:`)
	namedGroup  = regexp.MustCompile(`^\(\?P?<[A-Za-z_][A-Za-z0-9_]*>`)
)

func (p *reParser) group() (reNode, error) {
	rest := p.src[p.pos:]
	var opener, look string
	switch {
	case strings.HasPrefix(rest, "(?="), strings.HasPrefix(rest, "(?!"):
		opener, look = rest[:3], rest[2:3]
	case strings.HasPrefix(rest, "(?<="), strings.HasPrefix(rest, "(?<!"):
		opener, look = rest[:4], rest[2:4]
	case inlineFlags.MatchString(rest):
		text := inlineFlags.FindString(rest)
		p.pos += len(text)
		return reNode{kind: nodeFlags, text: text}, nil
	case flagGroup.MatchString(rest):
		opener = flagGroup.FindString(rest)
	case namedGroup.MatchString(rest):
		opener = namedGroup.FindString(rest)
	case strings.HasPrefix(rest, "(?"):
		return reNode{}, fmt.Errorf("unsupported group syntax at offset %d", p.pos)
	default:
		opener = "("
	}
	p.pos += len(opener)
	bodyStart := p.pos
	alts, err := p.alternatives()
	if err != nil {
		return reNode{}, err
	}
	if p.pos >= len(p.src) {
		return reNode{}, errors.New("missing )")
	}
	body := p.src[bodyStart:p.pos]
	p.pos++
	if look != "" {
		return reNode{kind: nodeLook, look: look, body: body, alts: alts}, nil
	}
	return reNode{kind: nodeGroup, open: opener, alts: alts}, nil
}

func (p *reParser) escape() error {
	p.pos++
	if p.pos >= len(p.src) {
		return errors.New("trailing backslash")
	}
	c := p.src[p.pos]
	if c >= '1' && c <= '9' {
		return errors.New("backreference")
	}
	_, size := utf8.DecodeRuneInString(p.src[p.pos:])
	p.pos += size
	for digits, prefix := range map[int]byte{4: 'u', 2: 'x'} {
		if c == prefix && hexDigits.MatchString(p.src[p.pos:]) && len(hexDigits.FindString(p.src[p.pos:])) >= digits {
			p.pos += digits
			return nil
		}
	}
	if strings.ContainsRune("pPxu", rune(c)) && p.pos < len(p.src) && p.src[p.pos] == '{' {
		end := strings.IndexByte(p.src[p.pos:], '}')
		if end < 0 {
			return errors.New("unterminated escape")
		}
		p.pos += end + 1
	}
	return nil
}

func (p *reParser) class() error {
	p.pos++
	if strings.HasPrefix(p.src[p.pos:], "^") {
		p.pos++
	}
	if strings.HasPrefix(p.src[p.pos:], "]") {
		// ECMAScript reads [] and [^] as empty and any-character classes;
		// RE2 reads the ] as a literal.
		return errors.New("class starting with ]")
	}
	for p.pos < len(p.src) {
		switch {
		case p.src[p.pos] == '\\':
			if err := p.escape(); err != nil {
				return err
			}
		case p.src[p.pos] == ']':
			p.pos++
			return nil
		case strings.HasPrefix(p.src[p.pos:], "[:"):
			end := strings.Index(p.src[p.pos:], ":]")
			if end < 0 {
				return errors.New("unterminated class")
			}
			p.pos += end + 2
		default:
			_, size := utf8.DecodeRuneInString(p.src[p.pos:])
			p.pos += size
		}
	}
	return errors.New("unterminated class")
}

var hexDigits = regexp.MustCompile(`^[0-9a-fA-F]+`)

var braceQuantifier = regexp.MustCompile(`^\{[0-9]+(?:,[0-9]*)?\}`)

func (p *reParser) quantifier() string {
	start := p.pos
	if p.pos >= len(p.src) {
		return ""
	}
	switch p.src[p.pos] {
	case '*', '+', '?':
		p.pos++
	case '{':
		q := braceQuantifier.FindString(p.src[p.pos:])
		if q == "" {
			return ""
		}
		p.pos += len(q)
	default:
		return ""
	}
	if p.pos < len(p.src) && (p.src[p.pos] == '?' || p.src[p.pos] == '+') {
		p.pos++
	}
	return p.src[start:p.pos]
}

func renderNodes(nodes []reNode) string {
	var b strings.Builder
	for _, n := range nodes {
		switch n.kind {
		case nodeBegin:
			b.WriteString("^")
		case nodeText, nodeFlags:
			b.WriteString(n.text)
		case nodeLook:
			b.WriteString("(?" + n.look + n.body + ")")
		case nodeGroup:
			b.WriteString(n.open)
			for i, alt := range n.alts {
				if i > 0 {
					b.WriteString("|")
				}
				b.WriteString(renderNodes(alt))
			}
			b.WriteString(")")
		}
		b.WriteString(n.quant)
	}
	return b.String()
}

func containsLookaround(nodes []reNode) bool {
	for _, n := range nodes {
		if n.kind == nodeLook {
			return true
		}
		if n.kind == nodeGroup {
			for _, alt := range n.alts {
				if containsLookaround(alt) {
					return true
				}
			}
		}
	}
	return false
}

// expandGroups splices unquantified groups that contain lookarounds into
// the surrounding sequence, so each lookaround's position can be found. A
// group with several alternatives is distributed: a(b|c)d becomes abd and
// acd. Without backreferences, capture groups do not affect what matches.
func expandGroups(nodes []reNode) ([][]reNode, error) {
	out := [][]reNode{nil}
	for _, n := range nodes {
		if n.kind != nodeGroup || !containsLookaround([]reNode{n}) {
			for i := range out {
				out[i] = append(out[i], n)
			}
			continue
		}
		if n.quant != "" {
			return nil, errors.New("lookaround inside a repeated group")
		}
		if n.open != "(" && n.open != "(?:" && !namedGroup.MatchString(n.open) {
			return nil, errors.New("lookaround inside a group with inline flags")
		}
		var options [][]reNode
		for _, alt := range n.alts {
			for _, x := range alt {
				if x.kind == nodeFlags {
					// Splicing would extend the flags past the group.
					return nil, errors.New("inline flags inside a group with lookarounds")
				}
			}
			expanded, err := expandGroups(alt)
			if err != nil {
				return nil, err
			}
			options = append(options, expanded...)
		}
		if len(out)*len(options) > maxExpandedAlternatives {
			return nil, errors.New("too many alternatives around lookarounds")
		}
		var next [][]reNode
		for _, prefix := range out {
			for _, option := range options {
				next = append(next, append(append([]reNode(nil), prefix...), option...))
			}
		}
		out = next
	}
	return out, nil
}

type lookCondition struct {
	look   string
	body   string
	offset int  // characters consumed before the lookaround
	fixed  bool // offset is the same for every match
	atEnd  bool // only $ follows, so the position is the end of the string
}

// decomposeAlternative translates one alternative without top-level |. It
// returns a nil schema when the alternative can never match.
func decomposeAlternative(nodes []reNode) (map[string]any, string, error) {
	nodes = append([]reNode(nil), nodes...)
	for i, n := range nodes {
		if n.kind != nodeLook {
			continue
		}
		re, err := syntax.Parse(re2Pattern(n.body), syntax.Perl)
		if err != nil {
			return nil, "", fmt.Errorf("lookaround body is incompatible with RE2: %w", err)
		}
		if n.look == "!" && anyCharacter(re) {
			// (?![\s\S]) only holds at the end of the string.
			nodes[i] = reNode{kind: nodeText, text: "$"}
		}
	}
	var consumed []reNode
	var looks []lookCondition
	offset, fixed := 0, true
	for i, n := range nodes {
		switch n.kind {
		case nodeFlags:
			return nil, "", errors.New("inline flags outside a lookaround")
		case nodeLook:
			looks = append(looks, lookCondition{look: n.look, body: re2Pattern(n.body), offset: offset, fixed: fixed, atEnd: onlyEndFollows(nodes[i+1:])})
		default:
			consumed = append(consumed, n)
			re, err := syntax.Parse(re2Pattern(renderNodes([]reNode{n})), syntax.Perl)
			if err != nil {
				return nil, "", fmt.Errorf("pattern is incompatible with RE2: %w", err)
			}
			if min, max := width(re); min == max && max >= 0 {
				offset += min
			} else {
				fixed = false
			}
		}
	}
	rest := re2Pattern(renderNodes(consumed))
	restRE, err := syntax.Parse(rest, syntax.Perl)
	if err != nil {
		return nil, "", fmt.Errorf("pattern is incompatible with RE2: %w", err)
	}
	anchored := beginsWithText(restRE)
	for _, l := range looks {
		if l.look == "=" && l.fixed && l.offset == 0 && beginsWithText(mustParse(l.body)) {
			anchored = true
		}
	}
	var conditions []any
	var startOnly []string // negated conditions that apply only to a match at 0
	for _, l := range looks {
		var pattern string
		switch {
		case l.atEnd && (l.look == "<=" || l.look == "<!"):
			pattern = "(?:" + l.body + ")$"
		case l.look == "<=" || l.look == "<!":
			w, max := width(mustParse(l.body))
			if !anchored || !l.fixed || w != max || w < 0 {
				return nil, "", errors.New("lookbehind at a variable position or with variable width")
			}
			if w > l.offset {
				if l.look == "<=" {
					return nil, "", nil
				}
				continue
			}
			pattern = atOffset(l.offset-w, l.body)
		case anchored && l.fixed:
			pattern = atOffset(l.offset, l.body)
		case !anchored && l.fixed && l.offset == 0 && l.look == "!" && beginsWithText(mustParse(l.body)):
			// A match can also start later, where ^ in the body fails and
			// the negative lookahead holds.
			startOnly = append(startOnly, "^(?:"+l.body+")")
			continue
		default:
			return nil, "", errors.New("lookahead at a variable position")
		}
		if l.look == "=" || l.look == "<=" {
			conditions = append(conditions, map[string]any{"pattern": pattern})
		} else {
			conditions = append(conditions, map[string]any{"not": map[string]any{"type": "string", "pattern": pattern}})
		}
	}
	superset := rest
	if anchored && !beginsWithText(restRE) {
		superset = "^(?:" + rest + ")"
	}
	if !anchored {
		if len(startOnly) == 0 {
			conditions = append([]any{map[string]any{"pattern": rest}}, conditions...)
		} else {
			atStart := []any{map[string]any{"pattern": "^(?:" + rest + ")"}}
			for _, p := range startOnly {
				atStart = append(atStart, map[string]any{"not": map[string]any{"type": "string", "pattern": p}})
			}
			later := map[string]any{"pattern": `^(?s:.)+(?:` + rest + ")"}
			conditions = append([]any{map[string]any{"anyOf": []any{map[string]any{"allOf": atStart}, later}}}, conditions...)
		}
	} else if !nullableAtStart(restRE) {
		conditions = append([]any{map[string]any{"pattern": superset}}, conditions...)
	}
	for _, c := range conditions {
		if err := compileCondition(c); err != nil {
			return nil, "", err
		}
	}
	switch len(conditions) {
	case 0:
		return map[string]any{}, superset, nil
	case 1:
		return conditions[0].(map[string]any), superset, nil
	}
	return map[string]any{"allOf": conditions}, superset, nil
}

func compileCondition(v any) error {
	var err error
	walkSchemaObjects(v, func(s map[string]any) {
		if p, ok := s["pattern"].(string); ok && err == nil {
			if _, e := regexp.Compile(p); e != nil {
				err = fmt.Errorf("translated pattern is incompatible with RE2: %w", e)
			}
		}
	})
	return err
}

func atOffset(offset int, body string) string {
	if offset == 0 {
		return "^(?:" + body + ")"
	}
	return `^(?s:.){` + strconv.Itoa(offset) + "}(?:" + body + ")"
}

func mustParse(s string) *syntax.Regexp {
	re, err := syntax.Parse(s, syntax.Perl)
	if err != nil {
		panic(err)
	}
	return re
}

func onlyEndFollows(nodes []reNode) bool {
	end := false
	for _, n := range nodes {
		switch {
		case n.kind == nodeLook:
		case n.kind == nodeText && n.text == "$" && n.quant == "":
			end = true
		default:
			return false
		}
	}
	return end
}

// width returns the minimum and maximum number of characters re matches;
// max is -1 when unbounded.
func width(re *syntax.Regexp) (int, int) {
	switch re.Op {
	case syntax.OpLiteral:
		return len(re.Rune), len(re.Rune)
	case syntax.OpCharClass, syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return 1, 1
	case syntax.OpCapture:
		return width(re.Sub[0])
	case syntax.OpStar:
		return 0, -1
	case syntax.OpPlus:
		min, _ := width(re.Sub[0])
		return min, -1
	case syntax.OpQuest:
		_, max := width(re.Sub[0])
		return 0, max
	case syntax.OpRepeat:
		min, max := width(re.Sub[0])
		if re.Max < 0 || max < 0 {
			return min * re.Min, -1
		}
		return min * re.Min, max * re.Max
	case syntax.OpConcat:
		min, max := 0, 0
		for _, sub := range re.Sub {
			a, b := width(sub)
			min += a
			if max >= 0 && b >= 0 {
				max += b
			} else {
				max = -1
			}
		}
		return min, max
	case syntax.OpAlternate:
		min, max := -1, 0
		for _, sub := range re.Sub {
			a, b := width(sub)
			if min < 0 || a < min {
				min = a
			}
			if max >= 0 && (b < 0 || b > max) {
				max = b
			}
		}
		return min, max
	case syntax.OpNoMatch:
		return 0, 0
	}
	// Empty matches and assertions.
	return 0, 0
}

// beginsWithText reports whether every match of re starts at the beginning
// of the string.
func beginsWithText(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpBeginText:
		return true
	case syntax.OpCapture:
		return beginsWithText(re.Sub[0])
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			if beginsWithText(sub) {
				return true
			}
			if min, max := width(sub); min != 0 || max != 0 {
				return false
			}
		}
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if !beginsWithText(sub) {
				return false
			}
		}
		return len(re.Sub) > 0
	}
	return false
}

// nullableAtStart conservatively reports whether re matches the empty
// string at offset 0 of every string, so ^(?:re) adds nothing.
func nullableAtStart(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpEmptyMatch, syntax.OpBeginText, syntax.OpBeginLine, syntax.OpStar, syntax.OpQuest:
		return true
	case syntax.OpRepeat:
		return re.Min == 0 || nullableAtStart(re.Sub[0])
	case syntax.OpCapture, syntax.OpPlus:
		return nullableAtStart(re.Sub[0])
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			if !nullableAtStart(sub) {
				return false
			}
		}
		return true
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if nullableAtStart(sub) {
				return true
			}
		}
	}
	return false
}

// anyCharacter reports whether re matches exactly one character of any kind,
// as [\s\S] does.
func anyCharacter(re *syntax.Regexp) bool {
	re = re.Simplify()
	for re.Op == syntax.OpCapture {
		re = re.Sub[0]
	}
	if re.Op == syntax.OpAnyChar {
		return true
	}
	return re.Op == syntax.OpCharClass && len(re.Rune) == 2 && re.Rune[0] == 0 && re.Rune[1] == utf8.MaxRune
}
