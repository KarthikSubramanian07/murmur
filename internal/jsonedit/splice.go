package jsonedit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// The functions below edit a JSON or JSONC file (JSON with comments and trailing commas, which
// OpenCode reads) as text: they change the bytes of one value, or add one member or one item, and
// leave every other byte as it was. Format, above, writes the whole file again in one indentation,
// which is right for a file its program rewrites whole anyway (Claude Code's) and wrong for one a
// person keeps by hand, aligned their way, with comments.

// node is a value's place in the text: [start, end) are its bytes. An object has its members, an
// array its items.
type node struct {
	start, end int
	kind       byte // '{', '[', or 0 for anything else
	members    []member
	items      []node
}

type member struct {
	key      string
	keyStart int
	value    node
}

type scanner struct {
	s   string
	pos int
}

var errSyntax = errors.New("not valid JSON")

// space skips whitespace and comments.
func (sc *scanner) space() error {
	for sc.pos < len(sc.s) {
		c := sc.s[sc.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			sc.pos++
		case strings.HasPrefix(sc.s[sc.pos:], "//"):
			if i := strings.IndexByte(sc.s[sc.pos:], '\n'); i >= 0 {
				sc.pos += i + 1
			} else {
				sc.pos = len(sc.s)
			}
		case strings.HasPrefix(sc.s[sc.pos:], "/*"):
			i := strings.Index(sc.s[sc.pos+2:], "*/")
			if i < 0 {
				return errSyntax
			}
			sc.pos += 2 + i + 2
		default:
			return nil
		}
	}
	return nil
}

func (sc *scanner) str() (string, error) {
	start := sc.pos
	if sc.pos >= len(sc.s) || sc.s[sc.pos] != '"' {
		return "", errSyntax
	}
	for i := sc.pos + 1; i < len(sc.s); i++ {
		switch sc.s[i] {
		case '\\':
			i++
		case '"':
			sc.pos = i + 1
			var out string
			if err := json.Unmarshal([]byte(sc.s[start:sc.pos]), &out); err != nil {
				return "", errSyntax
			}
			return out, nil
		case '\n':
			return "", errSyntax
		}
	}
	return "", errSyntax
}

func (sc *scanner) value(depth int) (node, error) {
	if depth > 200 {
		return node{}, errSyntax
	}
	if err := sc.space(); err != nil {
		return node{}, err
	}
	if sc.pos >= len(sc.s) {
		return node{}, errSyntax
	}
	n := node{start: sc.pos}
	switch c := sc.s[sc.pos]; {
	case c == '{' || c == '[':
		n.kind = c
		closing := byte('}')
		if c == '[' {
			closing = ']'
		}
		sc.pos++
		for {
			if err := sc.space(); err != nil {
				return node{}, err
			}
			if sc.pos < len(sc.s) && sc.s[sc.pos] == closing {
				sc.pos++
				n.end = sc.pos
				return n, nil
			}
			if c == '{' {
				keyStart := sc.pos
				key, err := sc.str()
				if err != nil {
					return node{}, err
				}
				if err := sc.space(); err != nil {
					return node{}, err
				}
				if sc.pos >= len(sc.s) || sc.s[sc.pos] != ':' {
					return node{}, errSyntax
				}
				sc.pos++
				v, err := sc.value(depth + 1)
				if err != nil {
					return node{}, err
				}
				n.members = append(n.members, member{key: key, keyStart: keyStart, value: v})
			} else {
				v, err := sc.value(depth + 1)
				if err != nil {
					return node{}, err
				}
				n.items = append(n.items, v)
			}
			if err := sc.space(); err != nil {
				return node{}, err
			}
			switch {
			case sc.pos < len(sc.s) && sc.s[sc.pos] == ',':
				sc.pos++ // a trailing comma before the closing bracket is JSONC's, and allowed
			case sc.pos < len(sc.s) && sc.s[sc.pos] == closing:
			default:
				return node{}, errSyntax
			}
		}
	case c == '"':
		if _, err := sc.str(); err != nil {
			return node{}, err
		}
	default:
		end := sc.pos
		for end < len(sc.s) && strings.IndexByte(" \t\r\n,}]/", sc.s[end]) < 0 {
			end++
		}
		var v any
		if end == sc.pos || json.Unmarshal([]byte(sc.s[sc.pos:end]), &v) != nil {
			return node{}, errSyntax
		}
		sc.pos = end
	}
	n.end = sc.pos
	return n, nil
}

// parseText reads the text's one value, which must be an object. Blank text is an empty object.
func parseText(text string) (node, error) {
	sc := &scanner{s: text}
	if err := sc.space(); err != nil {
		return node{}, err
	}
	if sc.pos == len(text) {
		return node{}, nil
	}
	n, err := sc.value(0)
	if err != nil {
		return node{}, err
	}
	if err := sc.space(); err != nil || sc.pos != len(text) {
		return node{}, errSyntax
	}
	if n.kind != '{' {
		return node{}, errors.New("not a JSON object")
	}
	return n, nil
}

// find is the member key of object n, refusing a key given twice: which of the two counts depends on
// the program reading the file.
func (n node) find(key string) (*member, error) {
	var found *member
	for i := range n.members {
		if n.members[i].key == key {
			if found != nil {
				return nil, fmt.Errorf("%q is in the file twice", key)
			}
			found = &n.members[i]
		}
	}
	return found, nil
}

// decode reads a value's text, comments and trailing commas left out.
func decode(text string) (any, error) {
	var v any
	err := json.Unmarshal([]byte(stripJSONC(text)), &v)
	return v, err
}

// stripJSONC is text as plain JSON: comments become spaces and trailing commas go.
func stripJSONC(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(text) && text[j] != '"' {
				if text[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(text) {
				j = len(text) - 1
			}
			b.WriteString(text[i : j+1])
			i = j
		case strings.HasPrefix(text[i:], "//"):
			for i < len(text) && text[i] != '\n' {
				i++
			}
			if i < len(text) {
				b.WriteByte('\n')
			}
		case strings.HasPrefix(text[i:], "/*"):
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			i += 2 + end + 1
			b.WriteByte(' ')
		case c == ',':
			// A trailing comma, with space or comments before the bracket, goes.
			sc := &scanner{s: text, pos: i + 1}
			_ = sc.space()
			if j := sc.pos; j < len(text) && (text[j] == '}' || text[j] == ']') {
				continue
			}
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// same reports whether the text of a value says what v does.
func same(text string, v any) bool {
	have, err := decode(text)
	if err != nil {
		return false
	}
	raw, err := marshal(v)
	if err != nil {
		return false
	}
	var want any
	if json.Unmarshal(raw, &want) != nil {
		return false
	}
	return reflect.DeepEqual(have, want)
}

func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

// render writes v for a place whose line starts with indent, nested by unit, with the file's newline.
func render(v any, indent, unit, nl string) (string, error) {
	raw, err := marshal(v)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw, indent, unit); err != nil {
		return "", err
	}
	return strings.ReplaceAll(out.String(), "\n", nl), nil
}

// lineIndent is the whitespace that begins the line pos is on, and whether only whitespace comes
// before pos on it.
func lineIndent(text string, pos int) (string, bool) {
	start := strings.LastIndexByte(text[:pos], '\n') + 1
	lead := text[start:pos]
	trimmed := strings.TrimLeft(lead, " \t")
	return lead[:len(lead)-len(trimmed)], trimmed == ""
}

func newline(text string) string {
	if strings.Contains(text, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

// insertInto adds a member (key != "" in an object) or an item (in an array) at the end of container
// n, laid out like the ones before it.
func insertInto(text string, n node, key string, v any) (string, error) {
	nl, unit := newline(text), indentOf([]byte(text))
	prefix := ""
	if key != "" {
		k, err := marshal(key)
		if err != nil {
			return "", err
		}
		prefix = string(k) + ": "
	}
	var lastStart, lastEnd int
	switch {
	case len(n.members) > 0:
		m := n.members[len(n.members)-1]
		lastStart, lastEnd = m.keyStart, m.value.end
	case len(n.items) > 0:
		it := n.items[len(n.items)-1]
		lastStart, lastEnd = it.start, it.end
	default:
		// Empty: the new one goes on a line of its own, inside, unless the whole file is on one line.
		if !strings.Contains(text, "\n") {
			val, err := marshal(v)
			if err != nil {
				return "", err
			}
			return text[:n.end-1] + prefix + string(val) + text[n.end-1:], nil
		}
		indent, _ := lineIndent(text, n.start)
		child := indent + unit
		val, err := render(v, child, unit, nl)
		if err != nil {
			return "", err
		}
		inside := text[n.start+1 : n.end-1] // whitespace, or comments, which stay
		if strings.TrimSpace(inside) == "" {
			inside = ""
		}
		return text[:n.start+1] + inside + nl + child + prefix + val + nl + indent + text[n.end-1:], nil
	}
	indent, own := lineIndent(text, lastStart)
	// After the last one's value comes its comma, if it has one (JSONC's trailing comma), then the
	// new one.
	sc := &scanner{s: text, pos: lastEnd}
	_ = sc.space()
	at, comma := lastEnd, ","
	if sc.pos < len(text) && text[sc.pos] == ',' {
		at, comma = sc.pos+1, ""
	}
	if !own {
		// The last one shares its line with the bracket or another: the new one follows on that line.
		val, err := marshal(v)
		if err != nil {
			return "", err
		}
		return text[:at] + comma + " " + prefix + string(val) + text[at:], nil
	}
	val, err := render(v, indent, unit, nl)
	if err != nil {
		return "", err
	}
	return text[:at] + comma + nl + indent + prefix + val + text[at:], nil
}

// nest is v under the keys of path, outermost first.
func nest(path []string, v any) any {
	for i := len(path) - 1; i >= 0; i-- {
		o := New()
		o.Set(path[i], v)
		v = o
	}
	return v
}

// SetIn returns text with v at path, a list of object keys from the top: the value that is there is
// replaced, in place, if it says something else, and a missing one is added, with any objects on the
// way to it, at the end of its object. changed is false, and out is text, when the value is there
// already. It refuses (an error) text that isn't a JSON or JSONC object, a path through something
// that isn't an object, and a key given twice on the way.
func SetIn(text string, path []string, v any) (out string, changed bool, err error) {
	if len(path) == 0 {
		return "", false, errors.New("no path")
	}
	root, err := parseText(text)
	if err != nil {
		return "", false, err
	}
	if root.kind == 0 { // blank text
		s, err := render(nest(path, v), "", "  ", "\n")
		if err != nil {
			return "", false, err
		}
		return s + "\n", true, nil
	}
	n := root
	for i, key := range path {
		m, err := n.find(key)
		if err != nil {
			return "", false, err
		}
		if m == nil {
			out, err := insertInto(text, n, key, nest(path[i+1:], v))
			return out, err == nil, err
		}
		if i == len(path)-1 {
			if same(text[m.value.start:m.value.end], v) {
				return text, false, nil
			}
			indent, _ := lineIndent(text, m.keyStart)
			val, err := render(v, indent, indentOf([]byte(text)), newline(text))
			if err != nil {
				return "", false, err
			}
			return text[:m.value.start] + val + text[m.value.end:], true, nil
		}
		if m.value.kind != '{' {
			return "", false, fmt.Errorf("%q isn't an object", strings.Join(path[:i+1], "."))
		}
		n = m.value
	}
	return text, false, nil // not reached
}

// AppendIn returns text with item last in the array at path: unchanged if it is last already, and
// otherwise added at the end, any copy of it earlier in the array taken out. Last, because a list of
// rules where the last that matches counts (OpenCode's permissions) would let a later rule override
// it. A missing array is made, with item in it. Its refusals are SetIn's, and a path that ends at
// something other than an array.
func AppendIn(text string, path []string, item any) (out string, changed bool, err error) {
	root, err := parseText(text)
	if err != nil {
		return "", false, err
	}
	n := root
	for i, key := range path {
		if n.kind == 0 {
			break
		}
		m, err := n.find(key)
		if err != nil {
			return "", false, err
		}
		if m == nil {
			break
		}
		if i == len(path)-1 {
			if m.value.kind != '[' {
				return "", false, fmt.Errorf("%q isn't a list", strings.Join(path, "."))
			}
			items := m.value.items
			if len(items) > 0 && same(text[items[len(items)-1].start:items[len(items)-1].end], item) {
				return text, false, nil
			}
			// An earlier copy goes, with its comma and the whitespace after it, and no more: a comment
			// between it and the next item is the user's, about that one (Codex's second review of
			// #136). From the end back, so that the offsets before it hold.
			removed := false
			for j := len(items) - 2; j >= 0; j-- {
				if !same(text[items[j].start:items[j].end], item) {
					continue
				}
				sc := &scanner{s: text, pos: items[j].end}
				_ = sc.space()
				if sc.pos < len(text) && text[sc.pos] == ',' {
					text = text[:sc.pos] + text[sc.pos+1:]
				}
				end := items[j].end
				for end < len(text) && strings.IndexByte(" \t\r\n", text[end]) >= 0 {
					end++
				}
				text = text[:items[j].start] + text[end:]
				removed = true
			}
			if removed {
				out, _, err := AppendIn(text, path, item)
				return out, err == nil, err
			}
			out, err := insertInto(text, m.value, "", item)
			return out, err == nil, err
		}
		n = m.value
	}
	return SetIn(text, path, []any{item})
}

// GetIn reads the value at path, as plain JSON. found is false if it isn't there.
func GetIn(text string, path []string) (raw string, found bool, err error) {
	n, err := parseText(text)
	if err != nil {
		return "", false, err
	}
	for i, key := range path {
		if n.kind != '{' {
			if i == 0 {
				return "", false, nil
			}
			return "", false, fmt.Errorf("%q isn't an object", strings.Join(path[:i], "."))
		}
		m, err := n.find(key)
		if err != nil || m == nil {
			return "", false, err
		}
		n = m.value
	}
	return stripJSONC(text[n.start:n.end]), true, nil
}

// DeleteIn returns text without the member at path: the member, its comma and the space after it go,
// and every other byte stays. changed is false, and out is text, when it isn't there, a path through
// something that isn't an object included: there is nothing of that name to take out. It refuses
// text that isn't a JSON or JSONC object, and a key given twice on the way.
func DeleteIn(text string, path []string) (out string, changed bool, err error) {
	if len(path) == 0 {
		return "", false, errors.New("no path")
	}
	root, err := parseText(text)
	if err != nil {
		return "", false, err
	}
	n, err := walk(root, path[:len(path)-1])
	if err != nil || n == nil {
		return text, false, err
	}
	for i := range n.members {
		if n.members[i].key == path[len(path)-1] {
			if _, err := n.find(path[len(path)-1]); err != nil {
				return "", false, err
			}
			return cut(text, *n, i), true, nil
		}
	}
	return text, false, nil
}

// RemoveIn returns text without any item of the array at path that says what item does. changed is
// false, and out is text, when there is none, or no array there. Its refusals are DeleteIn's.
func RemoveIn(text string, path []string, item any) (out string, changed bool, err error) {
	for {
		root, err := parseText(text)
		if err != nil {
			return "", false, err
		}
		n, err := walk(root, path)
		if err != nil {
			return "", false, err
		}
		if n == nil || n.kind != '[' {
			return text, changed, nil
		}
		at := -1
		for i, it := range n.items {
			if same(text[it.start:it.end], item) {
				at = i
				break
			}
		}
		if at < 0 {
			return text, changed, nil
		}
		text, changed = cut(text, *n, at), true
	}
}

// SetLastIn is SetIn with the member at path made the last of its object, moved there if it is
// elsewhere: for an object whose members are rules where the last match counts (OpenCode 1's
// "permission"). changed is false only when it is last already and says what v does.
func SetLastIn(text string, path []string, v any) (out string, changed bool, err error) {
	if len(path) == 0 {
		return "", false, errors.New("no path")
	}
	root, err := parseText(text)
	if err != nil {
		return "", false, err
	}
	if n, err := walk(root, path[:len(path)-1]); err != nil {
		return "", false, err
	} else if n != nil && n.kind == '{' {
		key := path[len(path)-1]
		m, err := n.find(key)
		if err != nil {
			return "", false, err
		}
		if m != nil {
			last := n.members[len(n.members)-1]
			if last.key == key && same(text[last.value.start:last.value.end], v) {
				return text, false, nil
			}
			if text, _, err = DeleteIn(text, path); err != nil {
				return "", false, err
			}
		}
	}
	out, _, err = SetIn(text, path, v)
	return out, err == nil, err
}

// walk is the value at path under object n, nil if it isn't there or something on the way isn't an
// object. A key given twice on the way is an error, as for SetIn.
func walk(n node, path []string) (*node, error) {
	for _, key := range path {
		if n.kind != '{' {
			return nil, nil
		}
		m, err := n.find(key)
		if err != nil || m == nil {
			return nil, err
		}
		n = m.value
	}
	if n.kind == 0 {
		return nil, nil // blank text
	}
	return &n, nil
}

// cut returns text without member or item i of container n. One with another after it goes with its
// comma, a comment on the same line after that, and the space up to the next, which takes its
// place. The last goes with the line it has to itself, if it has one, and with the comma before it
// unless it had a trailing comma of its own; a container left with only space inside becomes {} or [].
func cut(text string, n node, i int) string {
	type span struct{ start, end int }
	var spans []span
	for _, m := range n.members {
		spans = append(spans, span{m.keyStart, m.value.end})
	}
	for _, it := range n.items {
		spans = append(spans, span{it.start, it.end})
	}
	s := spans[i]
	start, end := s.start, s.end
	sc := &scanner{s: text, pos: s.end}
	_ = sc.space()
	ownComma := sc.pos < len(text) && text[sc.pos] == ','
	if ownComma {
		end = sc.pos + 1
		for end < len(text) && (text[end] == ' ' || text[end] == '\t') {
			end++
		}
		if strings.HasPrefix(text[end:], "//") {
			for end < len(text) && text[end] != '\n' && text[end] != '\r' {
				end++
			}
		}
	}
	if i < len(spans)-1 {
		for end < len(text) && strings.IndexByte(" \t\r\n", text[end]) >= 0 {
			end++
		}
		return text[:start] + text[end:]
	}
	if _, own := lineIndent(text, start); own {
		// Its line goes from its start, and with the newline that ends it if nothing else is on it.
		// Never the newline before it, which may end a // comment on the line above.
		start = strings.LastIndexByte(text[:start], '\n') + 1
		rest := end
		for rest < len(text) && (text[rest] == ' ' || text[rest] == '\t') {
			rest++
		}
		if strings.HasPrefix(text[rest:], "\r\n") {
			end = rest + 2
		} else if strings.HasPrefix(text[rest:], "\n") {
			end = rest + 1
		}
	} else {
		for start > n.start+1 && (text[start-1] == ' ' || text[start-1] == '\t') {
			start--
		}
	}
	closing := n.end - (end - start)
	text = text[:start] + text[end:]
	if !ownComma && i > 0 {
		sc := &scanner{s: text, pos: spans[i-1].end}
		_ = sc.space()
		if sc.pos < start && text[sc.pos] == ',' {
			text = text[:sc.pos] + text[sc.pos+1:]
			closing--
		}
	}
	if len(spans) == 1 && strings.TrimSpace(text[n.start+1:closing-1]) == "" {
		text = text[:n.start+1] + text[closing-1:]
	}
	return text
}
