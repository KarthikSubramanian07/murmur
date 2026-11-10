package jsonedit

import (
	"strings"
	"testing"
)

func server() *Object {
	o := New()
	o.Set("type", "local")
	o.Set("command", []any{"/bin/mur", "mcp"})
	return o
}

// A JSONC file keeps its comments, trailing commas and alignment: only the new member is new.
func TestSetInAddsWhereItBelongsAndKeepsTheRest(t *testing.T) {
	src := "{\n    // mine\n    \"$schema\":  \"https://opencode.ai/config.json\",\n    \"mcp\": {\n        \"servers\": {\n            \"other\": {\"type\": \"local\", \"command\": [\"o\"]}, /* kept */\n        },\n    },\n}\n"
	got, changed, err := SetIn(src, []string{"mcp", "servers", "murmur"}, server())
	if err != nil || !changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	want := "{\n    // mine\n    \"$schema\":  \"https://opencode.ai/config.json\",\n    \"mcp\": {\n        \"servers\": {\n            \"other\": {\"type\": \"local\", \"command\": [\"o\"]}," +
		"\n            \"murmur\": {\n                \"type\": \"local\",\n                \"command\": [\n                    \"/bin/mur\",\n                    \"mcp\"\n                ]\n            } /* kept */\n        },\n    },\n}\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	again, changed, err := SetIn(got, []string{"mcp", "servers", "murmur"}, server())
	if err != nil || changed || again != got {
		t.Errorf("a second set changed it (%v, %v)", changed, err)
	}
}

func TestSetInMakesWhatIsMissing(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"", "{\n  \"mcp\": {\n    \"servers\": {\n      \"murmur\": 1\n    }\n  }\n}\n"},
		{"{}\n", "{\n  \"mcp\": {\n    \"servers\": {\n      \"murmur\": 1\n    }\n  }\n}\n"},
		{"{\r\n  \"a\": true\r\n}\r\n", "{\r\n  \"a\": true,\r\n  \"mcp\": {\r\n    \"servers\": {\r\n      \"murmur\": 1\r\n    }\r\n  }\r\n}\r\n"},
		{"{\"a\": 1}", "{\"a\": 1, \"mcp\": {\"servers\":{\"murmur\":1}}}"},
		{"{\n  \"mcp\": {}\n}\n", "{\n  \"mcp\": {\n    \"servers\": {\n      \"murmur\": 1\n    }\n  }\n}\n"},
	} {
		got, changed, err := SetIn(tc.src, []string{"mcp", "servers", "murmur"}, 1)
		if err != nil || !changed || got != tc.want {
			t.Errorf("%q: got %q (%v, %v), want %q", tc.src, got, changed, err, tc.want)
		}
	}
}

// A value that says something else is replaced in place, and only it.
func TestSetInReplacesAStaleValue(t *testing.T) {
	src := "{\n  \"x\": 1,\n  \"s\": {\"murmur\": {\"command\": [\"/old\"]}},\n  \"y\": 2\n}\n"
	got, changed, err := SetIn(src, []string{"s", "murmur"}, map[string]any{"command": []any{"/new"}})
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if !strings.Contains(got, "/new") || strings.Contains(got, "/old") || !strings.HasPrefix(got, "{\n  \"x\": 1,\n  \"s\": {\"murmur\": {") || !strings.HasSuffix(got, "}},\n  \"y\": 2\n}\n") {
		t.Errorf("got:\n%s", got)
	}
}

func TestSetInRefusesWhatItCannotEdit(t *testing.T) {
	for _, src := range []string{`[1]`, `{"mcp": 3}`, `{"mcp": {"x": []}}`, `{"mcp": 1, "mcp": {}}`, `{"mcp": {"x": 1, "x": 2}}`, `{"a":`, `{"a": 1} x`, `{"a": /* open`} {
		if out, _, err := SetIn(src, []string{"mcp", "x", "y"}, 1); err == nil {
			t.Errorf("%q: no error, got %q", src, out)
		}
	}
}

func TestAppendInAddsOnce(t *testing.T) {
	rule := map[string]any{"action": "murmur_*", "resource": "*", "effect": "allow"}
	src := "{\n  \"permissions\": [\n    { \"action\": \"shell\", \"resource\": \"*\", \"effect\": \"ask\" },\n  ],\n}\n"
	got, changed, err := AppendIn(src, []string{"permissions"}, rule)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	want := "{\n  \"permissions\": [\n    { \"action\": \"shell\", \"resource\": \"*\", \"effect\": \"ask\" },\n    {\n      \"action\": \"murmur_*\",\n      \"effect\": \"allow\",\n      \"resource\": \"*\"\n    }\n  ],\n}\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if _, changed, err := AppendIn(got, []string{"permissions"}, rule); changed || err != nil {
		t.Errorf("appended twice (%v)", err)
	}
	if got, _, err := AppendIn("{}", []string{"permissions"}, 1); err != nil || got != `{"permissions": [1]}` {
		t.Errorf("a missing list: %q, %v", got, err)
	}
	if _, _, err := AppendIn(`{"permissions": {}}`, []string{"permissions"}, 1); err == nil {
		t.Error("appended to an object")
	}
}

func TestGetIn(t *testing.T) {
	raw, found, err := GetIn("{\"a\": {\"b\": [1, 2,], // c\n}}", []string{"a", "b"})
	if err != nil || !found || strings.ReplaceAll(raw, " ", "") != "[1,2]" {
		t.Errorf("%q %v %v", raw, found, err)
	}
	if _, found, err := GetIn(`{"a": {}}`, []string{"a", "b"}); found || err != nil {
		t.Errorf("%v %v", found, err)
	}
}

// An item that is in the list, but before a rule that would override it, is moved to the end.
func TestAppendInMovesTheItemLast(t *testing.T) {
	src := "{\n  \"p\": [\n    \"mine\",\n    \"x\",\n    \"deny-all\",\n    \"x\",\n    \"ask\"\n  ]\n}\n"
	got, changed, err := AppendIn(src, []string{"p"}, "x")
	want := "{\n  \"p\": [\n    \"mine\",\n    \"deny-all\",\n    \"ask\",\n    \"x\"\n  ]\n}\n"
	if err != nil || !changed || got != want {
		t.Errorf("got %q (%v, %v), want %q", got, changed, err, want)
	}
	if _, changed, _ := AppendIn(got, []string{"p"}, "x"); changed {
		t.Error("moved an item that was last already")
	}

	// A comment about the rule after it stays.
	src = "{\n  \"p\": [\n    \"x\",\n    // no one may do anything\n    \"deny-all\",\n  ],\n}\n"
	got, _, err = AppendIn(src, []string{"p"}, "x")
	want = "{\n  \"p\": [\n    // no one may do anything\n    \"deny-all\",\n    \"x\"\n  ],\n}\n"
	if err != nil || got != want {
		t.Errorf("got %q (%v), want %q", got, err, want)
	}
}

// DeleteIn takes out the one member, its comma with it, and leaves every other byte; one that isn't
// there is no change.
func TestDeleteIn(t *testing.T) {
	path := []string{"mcp", "murmur"}
	for _, tc := range []struct{ src, want string }{
		// first of two: the next moves up into its place
		{"{\n  \"mcp\": {\n    \"murmur\": 1,\n    \"other\": 2\n  }\n}\n", "{\n  \"mcp\": {\n    \"other\": 2\n  }\n}\n"},
		// last of two: its line goes, and the comma before it
		{"{\n  \"mcp\": {\n    \"other\": 2,\n    \"murmur\": 1\n  }\n}\n", "{\n  \"mcp\": {\n    \"other\": 2\n  }\n}\n"},
		// last with a trailing comma of its own (JSONC): the one before keeps its comma
		{"{\n  \"mcp\": {\n    \"other\": 2, // mine\n    \"murmur\": 1,\n  },\n}\n", "{\n  \"mcp\": {\n    \"other\": 2, // mine\n  },\n}\n"},
		// a comment on its line after its comma goes with it
		{"{\"mcp\": {\n  \"murmur\": 1, // join's\n  \"other\": 2\n}}", "{\"mcp\": {\n  \"other\": 2\n}}"},
		// the only one: the object is left empty
		{"{\n  \"mcp\": {\n    \"murmur\": {\"type\": \"local\"}\n  }\n}\n", "{\n  \"mcp\": {}\n}\n"},
		{"{\"mcp\": {\"murmur\": 1}, \"x\": 1}", "{\"mcp\": {}, \"x\": 1}"},
		{"{\"mcp\": {\"a\": 1, \"murmur\": 1}}", "{\"mcp\": {\"a\": 1}}"},
		// a comment beside it, inside, stays
		{"{\"mcp\": {\n  // keep\n  \"murmur\": 1\n}}", "{\"mcp\": {\n  // keep\n}}"},
		{"{\r\n  \"mcp\": {\r\n    \"a\": 1,\r\n    \"murmur\": 1\r\n  }\r\n}\r\n", "{\r\n  \"mcp\": {\r\n    \"a\": 1\r\n  }\r\n}\r\n"},
	} {
		got, changed, err := DeleteIn(tc.src, path)
		if err != nil || !changed || got != tc.want {
			t.Errorf("%q:\ngot  %q (%v, %v)\nwant %q", tc.src, got, changed, err, tc.want)
		}
		if _, err := parseText(got); err != nil {
			t.Errorf("%q: left %q, which isn't JSON: %v", tc.src, got, err)
		}
	}
	for _, src := range []string{"", "{}", "{\"mcp\": []}", "{\"mcp\": {\"other\": 1}}", "{\"x\": {\"murmur\": 1}}"} {
		if got, changed, err := DeleteIn(src, path); err != nil || changed || got != src {
			t.Errorf("%q: got %q (%v, %v)", src, got, changed, err)
		}
	}
	if _, _, err := DeleteIn("{\"mcp\": {\"murmur\": 1, \"murmur\": 2}}", path); err == nil {
		t.Error("a key given twice: no error")
	}
	if _, _, err := DeleteIn("[1]", path); err == nil {
		t.Error("not an object: no error")
	}
}

// RemoveIn takes every copy of the item out of the list, and the rest stays.
func TestRemoveIn(t *testing.T) {
	rule := map[string]any{"action": "murmur_*", "resource": "*", "effect": "allow"}
	src := "{\n  \"permissions\": [\n    {\"action\": \"murmur_*\", \"resource\": \"*\", \"effect\": \"allow\"},\n    {\"action\": \"*\", \"resource\": \"*\", \"effect\": \"ask\"},\n    {\"effect\": \"allow\", \"action\": \"murmur_*\", \"resource\": \"*\"}\n  ]\n}\n"
	got, changed, err := RemoveIn(src, []string{"permissions"}, rule)
	want := "{\n  \"permissions\": [\n    {\"action\": \"*\", \"resource\": \"*\", \"effect\": \"ask\"}\n  ]\n}\n"
	if err != nil || !changed || got != want {
		t.Errorf("got %q (%v, %v)\nwant %q", got, changed, err, want)
	}
	got, changed, err = RemoveIn("{\"permissions\": [{\"action\": \"murmur_*\", \"resource\": \"*\", \"effect\": \"allow\"}]}", []string{"permissions"}, rule)
	if err != nil || !changed || got != "{\"permissions\": []}" {
		t.Errorf("the only one: got %q (%v, %v)", got, changed, err)
	}
	for _, src := range []string{"{}", "{\"permissions\": {\"x\": 1}}", "{\"permissions\": [1]}"} {
		if got, changed, err := RemoveIn(src, []string{"permissions"}, rule); err != nil || changed || got != src {
			t.Errorf("%q: got %q (%v, %v)", src, got, changed, err)
		}
	}
}

// SetLastIn adds the member last, and moves it there from anywhere else, so a later rule can't
// override it.
func TestSetLastIn(t *testing.T) {
	path := []string{"permission", "murmur_*"}
	for _, tc := range []struct {
		src, want string
		changed   bool
	}{
		{"{}", "{\"permission\": {\"murmur_*\":\"allow\"}}", true},
		{"{\n  \"permission\": {\n    \"*\": \"ask\"\n  }\n}\n", "{\n  \"permission\": {\n    \"*\": \"ask\",\n    \"murmur_*\": \"allow\"\n  }\n}\n", true},
		{"{\n  \"permission\": {\n    \"murmur_*\": \"allow\",\n    \"*\": \"ask\"\n  }\n}\n", "{\n  \"permission\": {\n    \"*\": \"ask\",\n    \"murmur_*\": \"allow\"\n  }\n}\n", true},
		{"{\n  \"permission\": {\n    \"*\": \"ask\",\n    \"murmur_*\": \"deny\"\n  }\n}\n", "{\n  \"permission\": {\n    \"*\": \"ask\",\n    \"murmur_*\": \"allow\"\n  }\n}\n", true},
		{"{\n  \"permission\": {\n    \"*\": \"ask\",\n    \"murmur_*\": \"allow\"\n  }\n}\n", "{\n  \"permission\": {\n    \"*\": \"ask\",\n    \"murmur_*\": \"allow\"\n  }\n}\n", false},
	} {
		got, changed, err := SetLastIn(tc.src, path, "allow")
		if err != nil || changed != tc.changed || got != tc.want {
			t.Errorf("%q:\ngot  %q (%v, %v)\nwant %q", tc.src, got, changed, err, tc.want)
		}
	}
	if _, _, err := SetLastIn("{\"permission\": \"ask\"}", path, "allow"); err == nil {
		t.Error("a permission that isn't an object: no error")
	}
}

// Taking out the last member keeps the newline that ends a // comment on the line before it, which
// would otherwise swallow the closing brace (Sol's review of #396).
func TestDeleteInKeepsALineCommentClosed(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"{\"model\":\"x/y\", // keep\n \"permission\":{\"murmur_*\":\"allow\"}}", "{\"model\":\"x/y\" // keep\n}"},
		{"{\n  \"model\": \"x/y\", // keep\n  \"permission\": {}\n}\n", "{\n  \"model\": \"x/y\" // keep\n}\n"},
	} {
		got, changed, err := DeleteIn(tc.src, []string{"permission"})
		if err != nil || !changed || got != tc.want {
			t.Errorf("%q:\ngot  %q (%v, %v)\nwant %q", tc.src, got, changed, err, tc.want)
		}
		if _, err := parseText(got); err != nil {
			t.Errorf("%q: left %q, which doesn't parse: %v", tc.src, got, err)
		}
	}
}

// A trailing comma with a comment after it is still JSONC's trailing comma: the rule matches.
func TestRemoveInWithACommentAfterATrailingComma(t *testing.T) {
	rule := map[string]any{"action": "murmur_*", "resource": "*", "effect": "allow"}
	src := `{"permissions":[{"action":"murmur_*","resource":"*","effect":"allow", /* note */}]}`
	got, changed, err := RemoveIn(src, []string{"permissions"}, rule)
	if err != nil || !changed || got != `{"permissions":[]}` {
		t.Errorf("got %q (%v, %v)", got, changed, err)
	}
}
