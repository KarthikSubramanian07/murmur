package names

import (
	"bytes"
	"errors"
	"io"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
)

var wordPattern = regexp.MustCompile(`^[a-z]{3,9}$`)

// seeded returns a reproducible stream of pseudo-random bytes.
func seeded(seed byte) io.Reader {
	var key [32]byte
	key[0] = seed
	return rand.NewChaCha8(key)
}

func checkList(t *testing.T, kind string, words []string) {
	t.Helper()
	if len(words) < 1000 {
		t.Errorf("%s: %d words, want at least 1000", kind, len(words))
	}
	seen := make(map[string]bool, len(words))
	for _, w := range words {
		if !wordPattern.MatchString(w) {
			t.Errorf("%s: %q is not 3 to 9 lowercase ASCII letters", kind, w)
		}
		if seen[w] {
			t.Errorf("%s: %q appears twice", kind, w)
		}
		seen[w] = true
	}
}

func TestLists(t *testing.T) {
	checkList(t, "adjectives", adjectives)
	checkList(t, "nouns", nouns)
	for _, w := range adjectives {
		if _, ok := nounSet[w]; ok {
			t.Errorf("%q is in both lists", w)
		}
	}
	if Count() < 1_000_000 {
		t.Errorf("Count() = %d, want at least 1,000,000", Count())
	}
	if want := len(adjectives) * len(nouns); Count() != want {
		t.Errorf("Count() = %d, want %d", Count(), want)
	}
}

func TestListFilesAreOneWordPerLine(t *testing.T) {
	for kind, text := range map[string]string{"adjectives": adjectiveText, "nouns": nounText} {
		if strings.ContainsAny(text, " \t") {
			t.Errorf("%s: whitespace inside a line", kind)
		}
	}
}

func TestGenerateIsValid(t *testing.T) {
	r := seeded(1)
	for range 2000 {
		name, err := Generate(r)
		if err != nil {
			t.Fatal(err)
		}
		if !Valid(name) {
			t.Fatalf("Generate returned %q, which is not Valid", name)
		}
	}
}

func TestGenerateDeterministic(t *testing.T) {
	a, b := seeded(7), seeded(7)
	for range 100 {
		x, err := Generate(a)
		if err != nil {
			t.Fatal(err)
		}
		y, err := Generate(b)
		if err != nil {
			t.Fatal(err)
		}
		if x != y {
			t.Fatalf("same reader state gave %q and %q", x, y)
		}
	}
	x, _ := Generate(seeded(1))
	y, _ := Generate(seeded(2))
	if x == y {
		t.Errorf("different seeds both gave %q", x)
	}
}

func TestGenerateMapsBytesToWords(t *testing.T) {
	// Index 0 of each list, then index 1 of each list, as big-endian 32-bit draws.
	r := bytes.NewReader([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 1})
	if got, want := mustGenerate(t, r), adjectives[0]+"-"+nouns[0]; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := mustGenerate(t, r), adjectives[1]+"-"+nouns[1]; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func mustGenerate(t *testing.T, r io.Reader) string {
	t.Helper()
	name, err := Generate(r)
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func TestGenerateReaderErrors(t *testing.T) {
	boom := errors.New("boom")
	for name, r := range map[string]io.Reader{
		"empty":          bytes.NewReader(nil),
		"short":          bytes.NewReader([]byte{1, 2, 3}),
		"only adjective": bytes.NewReader(make([]byte, 4)),
		"failing":        io.MultiReader(bytes.NewReader(make([]byte, 4)), errReader{boom}),
	} {
		got, err := Generate(r)
		if err == nil {
			t.Errorf("%s: Generate returned %q and no error", name, got)
		}
		if got != "" {
			t.Errorf("%s: Generate returned %q alongside an error", name, got)
		}
	}
	if _, err := Generate(errReader{boom}); !errors.Is(err, boom) {
		t.Errorf("error %v does not wrap the reader's", err)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestValid(t *testing.T) {
	adj, noun := adjectives[0], nouns[0]
	for _, name := range []string{adj + "-" + noun, adjectives[len(adjectives)-1] + "-" + nouns[len(nouns)-1]} {
		if !Valid(name) {
			t.Errorf("Valid(%q) = false", name)
		}
	}
	for name, s := range map[string]string{
		"empty":           "",
		"hyphen only":     "-",
		"one word":        adj,
		"one word noun":   noun,
		"trailing hyphen": adj + "-",
		"leading hyphen":  "-" + noun,
		"three words":     adj + "-" + noun + "-" + noun,
		"swapped":         noun + "-" + adj,
		"uppercase":       strings.ToUpper(adj + "-" + noun),
		"capitalised":     strings.ToUpper(adj[:1]) + adj[1:] + "-" + noun,
		"unknown adj":     "zzzzz-" + noun,
		"unknown noun":    adj + "-zzzzz",
		"underscore":      adj + "_" + noun,
		"space":           adj + " " + noun,
		"padded":          " " + adj + "-" + noun,
		"newline":         adj + "-" + noun + "\n",
		"two nouns":       noun + "-" + noun,
	} {
		if Valid(s) {
			t.Errorf("Valid(%q) = true (%s)", s, name)
		}
	}
}

func TestPickSkipsBiasedTail(t *testing.T) {
	const n = 1144
	limit := uint64(1<<32) - uint64(1<<32)%n
	if limit == 1<<32 {
		t.Skip("n divides 2^32; no biased tail")
	}
	// 0xFFFFFFFF is in the tail and must be discarded; the next draw, 5, is used.
	r := bytes.NewReader([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0, 0, 0, 5})
	got, err := pick(r, n)
	if err != nil {
		t.Fatal(err)
	}
	if got != 5 {
		t.Errorf("pick = %d, want 5 (tail draw should have been rejected)", got)
	}
	// The last accepted draw is still accepted.
	r = bytes.NewReader([]byte{byte((limit - 1) >> 24), byte((limit - 1) >> 16), byte((limit - 1) >> 8), byte(limit - 1)})
	if got, err = pick(r, n); err != nil || got != n-1 {
		t.Errorf("pick(last accepted draw) = %d, %v; want %d", got, err, n-1)
	}
}

func TestPickUniform(t *testing.T) {
	const n, draws = 10, 100_000
	r := seeded(3)
	counts := make([]int, n)
	for range draws {
		i, err := pick(r, n)
		if err != nil {
			t.Fatal(err)
		}
		counts[i]++
	}
	for i, c := range counts {
		// Expected 10,000 with a standard deviation near 95; this allows about ten.
		if c < 9000 || c > 11000 {
			t.Errorf("index %d drawn %d times of %d", i, c, draws)
		}
	}
}

func TestGenerateUniform(t *testing.T) {
	const draws = 300_000
	r := seeded(4)
	adjCounts := map[string]int{}
	nounCounts := map[string]int{}
	for range draws {
		adj, noun, _ := strings.Cut(mustGenerate(t, r), "-")
		adjCounts[adj]++
		nounCounts[noun]++
	}
	check := func(kind string, words []string, counts map[string]int) {
		want := float64(draws) / float64(len(words))
		for _, w := range words {
			// Each count is binomial with a standard deviation near sqrt(want), about 16 to 17
			// here; this allows eight or more.
			if c := float64(counts[w]); c < want/2 || c > want*3/2 {
				t.Errorf("%s %q drawn %d times, expected about %.0f", kind, w, counts[w], want)
			}
		}
	}
	check("adjective", adjectives, adjCounts)
	check("noun", nouns, nounCounts)
}
