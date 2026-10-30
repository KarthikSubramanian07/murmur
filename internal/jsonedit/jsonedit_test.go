package jsonedit

import (
	"strings"
	"testing"
)

// An edit leaves every other key where it was, with its value as written.
func TestEditKeepsOrderAndValues(t *testing.T) {
	src := "{\n    \"zeta\": 1,\n    \"alpha\": {\"b\": [1, 2.50, \"<&>\"], \"a\": null},\n    \"mid\": true\n}\n"
	o, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	o.Object("alpha").Set("c", "new")
	o.Delete("mid")
	got, err := Format(o, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n    \"zeta\": 1,\n    \"alpha\": {\n        \"b\": [\n            1,\n            2.50,\n            \"<&>\"\n        ],\n        \"a\": null,\n        \"c\": \"new\"\n    }\n}\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestParseRejectsWhatIsNotAnObject(t *testing.T) {
	for _, src := range []string{`[1]`, `"x"`, `{"a":`, `{"a":1} {"b":2}`, `{"a":1,}`} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%q parsed", src)
		}
	}
	if o, err := Parse([]byte("  \n")); err != nil || o.Len() != 0 {
		t.Errorf("a blank file is an empty object, got %v, %v", o, err)
	}
}

func TestFormatKeepsCRLF(t *testing.T) {
	src := "{\r\n  \"a\": 1\r\n}\r\n"
	o, _ := Parse([]byte(src))
	o.Set("b", 2)
	got, _ := Format(o, []byte(src))
	if strings.Count(string(got), "\n") != strings.Count(string(got), "\r\n") || !strings.Contains(string(got), `"b": 2`) {
		t.Errorf("got %q", got)
	}
}
