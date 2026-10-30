// Package jsonedit reads and writes JSON config files that belong to someone else (a user's
// Claude Code settings, a repo's .mcp.json) without reordering their keys. Go's maps sort keys on
// the way out, which would make every edit of a committed file a diff of the whole file.
package jsonedit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Object is a JSON object that remembers the order of its keys. Its values are nested *Object,
// []any, and the scalars encoding/json produces (json.Number for numbers). The methods that only
// read accept a nil *Object and see an empty one, so a lookup through a missing key needs no checks.
type Object struct {
	keys []string
	vals map[string]any
}

// New returns an empty object.
func New() *Object { return &Object{vals: map[string]any{}} }

// Len is the number of keys.
func (o *Object) Len() int {
	if o == nil {
		return 0
	}
	return len(o.keys)
}

// Get returns the value at key.
func (o *Object) Get(key string) (any, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.vals[key]
	return v, ok
}

// Object returns the nested object at key, or nil if the key is absent or holds something else.
func (o *Object) Object(key string) *Object {
	if o == nil {
		return nil
	}
	v, _ := o.vals[key].(*Object)
	return v
}

// Set puts a value at key, at the end if the key is new and in place otherwise.
func (o *Object) Set(key string, v any) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

// Delete removes key.
func (o *Object) Delete(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// Keys lists the keys in file order.
func (o *Object) Keys() []string {
	if o == nil {
		return nil
	}
	return append([]string(nil), o.keys...)
}

// MarshalJSON writes the keys in order.
func (o *Object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := encode(k)
		if err != nil {
			return nil, err
		}
		vb, err := encode(o.vals[k])
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

// Parse reads a JSON object. Empty input is an empty object, as a config file that exists but is
// blank is not an error to the programs that read it.
func Parse(b []byte) (*Object, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return New(), nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, err := value(dec)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*Object)
	if !ok {
		return nil, errors.New("not a JSON object")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON object")
	}
	return o, nil
}

func value(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch d {
	case '{':
		o := New()
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := kt.(string)
			if !ok {
				return nil, fmt.Errorf("unexpected key %v", kt)
			}
			v, err := value(dec)
			if err != nil {
				return nil, err
			}
			o.Set(key, v) // a repeated key keeps its first place and its last value, as most parsers do
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return o, nil
	case '[':
		arr := []any{}
		for dec.More() {
			v, err := value(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return arr, nil
	}
	return nil, fmt.Errorf("unexpected %v", d)
}

// Format writes o indented like the file it came from: with the indent that file used, if any
// (original may be nil), else two spaces, and with a trailing newline.
func Format(o *Object, original []byte) ([]byte, error) {
	b, err := o.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, b, "", indentOf(original)); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	if bytes.Contains(original, []byte("\r\n")) {
		return bytes.ReplaceAll(out.Bytes(), []byte("\n"), []byte("\r\n")), nil
	}
	return out.Bytes(), nil
}

// indentOf is the whitespace in front of the first indented line of a JSON file.
func indentOf(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed != "" && trimmed != line {
			return line[:len(line)-len(trimmed)]
		}
	}
	return "  "
}

// ReadFile reads a JSON object from a file. A missing file is an empty object, so a caller that
// edits and writes back creates it; raw is nil then.
func ReadFile(path string) (o *Object, raw []byte, err error) {
	raw, err = os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return New(), nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	o, err = Parse(raw)
	if err != nil {
		return nil, nil, err
	}
	return o, raw, nil
}
