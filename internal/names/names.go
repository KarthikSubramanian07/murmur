// Package names generates the two-word session names (`quiet-otter`) that #20 makes each
// session's address. It only generates and validates: whether a name is already taken in the repo
// is decided elsewhere, by compare-and-swap on a shared ref.
//
// The word lists are plain text under this directory, one word per line, embedded in the binary.
// SOURCES says where they came from.
package names

import (
	_ "embed"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

//go:embed adjectives.txt
var adjectiveText string

//go:embed nouns.txt
var nounText string

var (
	adjectives = parse(adjectiveText)
	nouns      = parse(nounText)

	adjectiveSet = set(adjectives)
	nounSet      = set(nouns)
)

// parse splits a list file into words. It tolerates CRLF, because a checkout with autocrlf
// rewrites the file before it is embedded.
func parse(text string) []string {
	var words []string
	for _, line := range strings.Split(text, "\n") {
		if w := strings.TrimSpace(line); w != "" {
			words = append(words, w)
		}
	}
	return words
}

func set(words []string) map[string]struct{} {
	m := make(map[string]struct{}, len(words))
	for _, w := range words {
		m[w] = struct{}{}
	}
	return m
}

// Count returns how many distinct names Generate can produce.
func Count() int { return len(adjectives) * len(nouns) }

// Generate returns a name of the form adjective-noun, each word chosen uniformly using bytes read
// from r (crypto/rand.Reader in production). It returns r's error if r cannot supply them.
func Generate(r io.Reader) (string, error) {
	a, err := pick(r, len(adjectives))
	if err != nil {
		return "", fmt.Errorf("names: choosing adjective: %w", err)
	}
	n, err := pick(r, len(nouns))
	if err != nil {
		return "", fmt.Errorf("names: choosing noun: %w", err)
	}
	return adjectives[a] + "-" + nouns[n], nil
}

// Valid reports whether name is exactly an adjective and a noun from the lists, joined by a hyphen.
func Valid(name string) bool {
	adj, noun, ok := strings.Cut(name, "-")
	if !ok {
		return false
	}
	if _, ok := adjectiveSet[adj]; !ok {
		return false
	}
	_, ok = nounSet[noun]
	return ok
}

// pick returns a uniform integer in [0, n). It draws 32 bits at a time and discards draws from the
// uneven tail of the range, so no index is favoured the way `x % n` would favour the low ones.
func pick(r io.Reader, n int) (int, error) {
	const space uint64 = 1 << 32
	limit := space - space%uint64(n)
	var buf [4]byte
	for {
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, err
		}
		if v := uint64(binary.BigEndian.Uint32(buf[:])); v < limit {
			return int(v % uint64(n)), nil
		}
	}
}
