package tags

import (
	"slices"
	"testing"
	"time"
)

func TestModelTag(t *testing.T) {
	for id, want := range map[string]string{
		"claude-opus-5-5":            "model:opus-5.5",
		"claude-sonnet-4-5-20250929": "model:sonnet-4.5",
		"claude-haiku-5-5":           "model:haiku-5.5",
		"gpt-5.1-codex":              "model:gpt-5.1-codex",
		"GPT-5":                      "model:gpt-5",
		"  ":                         "",
		"some model/with spaces":     "model:some-model-with-spaces",
	} {
		if got := ModelTag(id); got != want {
			t.Errorf("ModelTag(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestValid(t *testing.T) {
	for _, ok := range []string{"reviewing", "model:opus-5.5", "watch:branch/main", "unavailable", "a"} {
		if !Valid(ok) {
			t.Errorf("Valid(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "Reviewing", "-x", ":x", "a b", "available", "model:", "ünicode",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if Valid(bad) {
			t.Errorf("Valid(%q) = true", bad)
		}
	}
}

func TestSetSelf(t *testing.T) {
	var st State
	if err := st.SetSelf([]string{"Reviewing", "model:opus-5.5"}, nil); err != nil {
		t.Fatal(err)
	}
	// A second model replaces the first; available takes unavailable away.
	if err := st.SetSelf([]string{"model:sonnet-5.5", "unavailable"}, nil); err != nil {
		t.Fatal(err)
	}
	if want := []string{"model:sonnet-5.5", "reviewing", "unavailable"}; !slices.Equal(st.Self, want) {
		t.Fatalf("Self = %v, want %v", st.Self, want)
	}
	if err := st.SetSelf([]string{"available"}, []string{"reviewing"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"model:sonnet-5.5"}; !slices.Equal(st.Self, want) {
		t.Fatalf("Self = %v, want %v", st.Self, want)
	}
	if err := st.SetSelf([]string{"no spaces allowed"}, nil); err == nil {
		t.Fatal("a tag with spaces was taken")
	}
	var many []string
	for _, c := range "abcdefghijklm" {
		many = append(many, string(c))
	}
	if err := st.SetSelf(many, nil); err == nil {
		t.Fatalf("%d tags were taken", len(many)+1)
	}
	if want := []string{"model:sonnet-5.5"}; !slices.Equal(st.Self, want) {
		t.Fatalf("a refused change changed Self to %v", st.Self)
	}
}

func TestEffectivePrecedence(t *testing.T) {
	at := time.Now()
	var st State
	st.SetSelf([]string{"reviewing"}, nil)
	derived := []string{"model:opus-5.5"}

	got := Effective(st.Self, derived, st.Human)
	want := []Tag{{"model:opus-5.5", SrcDerived}, {"reviewing", SrcSession}}
	if !slices.Equal(got, want) {
		t.Fatalf("Effective = %v, want %v", got, want)
	}

	// The session's own model wins over the derived one.
	st.SetSelf([]string{"model:sonnet-5.5"}, nil)
	got = Effective(st.Self, derived, st.Human)
	if !Has(got, "model:sonnet-5.5") || Has(got, "model:opus-5.5") {
		t.Fatalf("Effective = %v: the session's model should replace the derived one", got)
	}

	// A human beats both: unavailable on, reviewing off, and the model replaced.
	base := Base(st.Self, derived)
	if err := st.SetHuman(base, []string{"unavailable", "model:haiku-5.5"}, []string{"reviewing"}, "tyler", at); err != nil {
		t.Fatal(err)
	}
	got = Effective(st.Self, derived, st.Human)
	want = []Tag{{"unavailable", SrcHuman}, {"model:haiku-5.5", SrcHuman}}
	if !slices.Equal(got, want) {
		t.Fatalf("Effective = %v, want %v", got, want)
	}

	// The session adding reviewing back doesn't undo the human's mark.
	st.SetSelf([]string{"reviewing"}, nil)
	if Has(Effective(st.Self, derived, st.Human), "reviewing") {
		t.Fatal("the session overrode a human")
	}

	// A human removing a tag only a human gave leaves no mark; available clears unavailable.
	base = Base(st.Self, derived)
	if err := st.SetHuman(base, []string{"available"}, nil, "tyler", at); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Human[Unavailable]; ok {
		t.Fatalf("Human = %v: unavailable, which only a human set, left a mark", st.Human)
	}
}

func TestFilter(t *testing.T) {
	ts := []Tag{{Name: "model:opus-5.5"}, {Name: "reviewing"}}
	for _, c := range []struct {
		terms []string
		want  bool
	}{
		{nil, true},
		{[]string{"reviewing"}, true},
		{[]string{"reviewing", "model:opus*"}, true},
		{[]string{"reviewing,model:sonnet*"}, false},
		{[]string{"-reviewing"}, false},
		{[]string{"available"}, true},
		{[]string{"unavailable"}, false},
		{[]string{"-available"}, false},
		{[]string{"model:*", "-ui"}, true},
	} {
		f, err := ParseFilter(c.terms)
		if err != nil {
			t.Fatalf("ParseFilter(%v): %v", c.terms, err)
		}
		if got := f.Match(ts); got != c.want {
			t.Errorf("filter %v matched %v, want %v", c.terms, got, c.want)
		}
	}
	unavail := append(ts, Tag{Name: Unavailable, Src: SrcHuman})
	f, _ := ParseFilter([]string{"available"})
	if f.Match(unavail) {
		t.Error("available matched an unavailable session")
	}
	if _, err := ParseFilter([]string{"a b"}); err == nil {
		t.Error("a term with a space was taken")
	}
	if got := f.String(); got != "available" {
		t.Errorf("String() = %q", got)
	}
}

func TestCleanRead(t *testing.T) {
	got := CleanRead([]Tag{{Name: "ok"}, {Name: "ok"}, {Name: "Bad"}, {Name: "x", Src: "forged"}})
	want := []Tag{{Name: "ok"}, {Name: "x"}}
	if !slices.Equal(got, want) {
		t.Fatalf("CleanRead = %v, want %v", got, want)
	}
}
