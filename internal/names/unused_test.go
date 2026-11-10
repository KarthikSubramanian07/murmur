package names

import "testing"

// With nearly every name used, random draws miss, and Unused still finds the one name left. With
// none left it says so, rather than failing as a reader would.
func TestUnusedFindsTheLastFreeName(t *testing.T) {
	free := adjectives[len(adjectives)/2] + "-" + nouns[7]
	got, err := Unused(seeded(1), 1000, func(name string) bool { return name != free })
	if err != nil || got != free {
		t.Errorf("Unused = %q, %v; want %q", got, err, free)
	}

	got, err = Unused(seeded(2), 10, func(string) bool { return true })
	if err != nil || got != "" {
		t.Errorf("with every name used, Unused = %q, %v; want none", got, err)
	}
}

// While random draws find a name, Unused gives the one Generate would.
func TestUnusedDrawsAtRandomFirst(t *testing.T) {
	want := mustGenerate(t, seeded(3))
	got, err := Unused(seeded(3), 1000, func(string) bool { return false })
	if err != nil || got != want {
		t.Errorf("Unused = %q, %v; want %q", got, err, want)
	}
}
