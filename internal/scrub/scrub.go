// Package scrub blocks secrets from reaching the bus. Everything sent through Murmur stays in the
// repo's history, readable by anyone with read access, so a match blocks the send instead of
// being redacted: the agent learns what happened and can send again without it.
//
// The list is short and Murmur's own (docs/VISION.md, "Leaked secrets"). It aims at things agents
// actually paste: API keys, tokens, private keys, credentials in URLs and .env lines.
package scrub

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

type pattern struct {
	name string
	re   *regexp.Regexp
	// secret, when set, decides whether a match is one: re finds the shape, and secret tells a
	// credential in it from prose or a placeholder in the same shape. It gets re's submatches.
	secret func(m []string) bool
}

// at starts a key that begins with a fixed prefix: the start of the text or any character but a
// letter or a digit. `\b` would miss a key right after an underscore (`my_sk-ant-…`), since an
// underscore is a word character.
const at = `(?:^|[^A-Za-z0-9])`

// patterns are tried in order and the first match names the error, so a more specific kind has
// to come before a broader pattern that also matches it. An OpenRouter key (`sk-or-v1-` and 64
// hex characters) is such a case: the broad OpenAI-style pattern matches it too, and would have
// the error call it an OpenAI key. The OpenRouter pattern therefore sits above that one, the
// same way the Anthropic pattern already does.
var patterns = []pattern{
	{name: "a private key", re: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY( BLOCK)?-----`)}, // PEM, and PGP's BLOCK
	{name: "an AWS access key", re: regexp.MustCompile(at + `(AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{name: "a GitHub token", re: regexp.MustCompile(at + `(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36,}|` + at + `github_pat_[A-Za-z0-9_]{50,}`)},
	{name: "a GitLab personal access token", re: regexp.MustCompile(at + `glpat-[A-Za-z0-9_-]{20,}`)},
	{name: "an npm token", re: regexp.MustCompile(at + `npm_[A-Za-z0-9]{36}`)},
	{name: "a Hugging Face token", re: regexp.MustCompile(at + `hf_[A-Za-z]{34,}`)},
	{name: "an Anthropic API key", re: regexp.MustCompile(at + `sk-ant-[A-Za-z0-9_-]{20,}`)},
	{name: "an OpenRouter API key", re: regexp.MustCompile(at + `sk-or-v1-[0-9a-fA-F]{64}`)},
	{name: "an OpenAI-style API key", re: regexp.MustCompile(at + `sk-(proj-|svcacct-)?[A-Za-z0-9_-]{32,}`)},
	{name: "a Stripe key", re: regexp.MustCompile(at + `[rs]k_(live|test)_[A-Za-z0-9]{20,}`)},
	{name: "a Slack token", re: regexp.MustCompile(at + `xox[abposr]-[A-Za-z0-9-]{10,}`)},
	{name: "a Slack webhook URL", re: regexp.MustCompile(`hooks\.slack\.com/services/T[A-Z0-9]+/B[A-Z0-9]+/[A-Za-z0-9]{16,}`)},
	{name: "a Google API key", re: regexp.MustCompile(at + `AIza[0-9A-Za-z_-]{35}`)},
	{name: "a Google OAuth token", re: regexp.MustCompile(at + `ya29\.[0-9A-Za-z_-]{20,}`)},
	{name: "an Azure storage account key", re: regexp.MustCompile(`AccountKey=[A-Za-z0-9+/]{40,}`)},
	{name: "a JSON web token", re: regexp.MustCompile(at + `eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	// A bearer token is opaque, so the word alone says nothing: "Bearer YOUR_TOKEN_HERE" or a
	// long word in prose passes, and only a token mixing letters and digits is one.
	{name: "a bearer token", re: regexp.MustCompile(`(?i)\bbearer\s+([A-Za-z0-9._~+/-]{20,}=*)`),
		secret: func(m []string) bool { return lettersAndDigits(m[1]) }},
	{name: "a password in a URL", re: regexp.MustCompile(`\b[a-z][a-z0-9+.-]*://[^\s:/@]+:[^\s@/]{3,}@`)},
	// A password with a slash in it, which the pattern above stops at. A port and a path
	// (`localhost:5173/@vite/client`) has the same shape, so the part before the slash mustn't be
	// a number, the part before the @ mustn't end a path, and the password has to mix letters and
	// digits.
	{name: "a password in a URL", re: regexp.MustCompile(`\b[a-z][a-z0-9+.-]*://[^\s:/@]+:([^\s@/]+)(/[^\s@]*)@[A-Za-z0-9]`),
		secret: func(m []string) bool {
			return strings.Trim(m[1], "0123456789") != "" && !strings.HasSuffix(m[2], "/") && lettersAndDigits(m[1]+m[2])
		}},
}

// assignment finds NAME=value lines whose name says secret. The value is checked separately
// (see looksSecret), so that `API_KEY = process.env.API_KEY` in a code discussion passes.
var assignment = regexp.MustCompile(`(?i)\b[A-Z0-9_]*(SECRET|TOKEN|PASSWORD|PASSWD|API_?KEY|PRIVATE_?KEY|ACCESS_?KEY)[A-Z0-9_]*["']?\s*[:=]\s*["']?([^\s"']+)`)

// Check returns an error naming the kind of secret if any text looks like one.
func Check(texts ...string) error {
	for _, t := range texts {
		for _, p := range patterns {
			if p.secret == nil {
				if p.re.MatchString(t) {
					return blocked(p.name)
				}
				continue
			}
			for _, m := range p.re.FindAllStringSubmatch(t, -1) {
				if p.secret(m) {
					return blocked(p.name)
				}
			}
		}
		for _, m := range assignment.FindAllStringSubmatch(t, -1) {
			if looksSecret(m[2]) {
				return blocked("a credential assignment")
			}
		}
	}
	return nil
}

func blocked(what string) error {
	return fmt.Errorf("not sent: this contains what looks like %s. Everything sent through the bus "+
		"stays in the repo's history; remove it and send again", what)
}

// looksSecret says whether an assigned value looks like a real credential rather than code or
// a placeholder: long, token-shaped, and mixing letters with digits. A password's own punctuation,
// ! @ #, counts as token-shaped. & * % don't: they run a value into the next parameter of a query
// string (token_type=bearer&expires_in=3600), mask a key (sk-****1234), or name a Windows
// variable (%API_KEY%).
func looksSecret(v string) bool {
	if len(v) < 12 {
		return false
	}
	for _, r := range v {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("-_+/=.~!@#", r) {
			return false // parentheses, $, <, {: code or a placeholder
		}
	}
	return lettersAndDigits(v)
}

// lettersAndDigits reports whether s has both, as a key does and a word or a placeholder doesn't.
func lettersAndDigits(s string) bool {
	return strings.IndexFunc(s, unicode.IsLetter) >= 0 && strings.IndexFunc(s, unicode.IsDigit) >= 0
}
