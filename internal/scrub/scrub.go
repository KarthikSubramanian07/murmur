// Package scrub blocks secrets from reaching the bus. Everything sent through t4 stays in the
// repo's history, readable by anyone with read access, so a match blocks the send instead of
// being redacted: the agent learns what happened and can send again without it.
//
// The list is short and t4's own (docs/VISION.md, "Leaked secrets"). It aims at things agents
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
}

var patterns = []pattern{
	{"a private key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"an AWS access key", regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"a GitHub token", regexp.MustCompile(`\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36,}|\bgithub_pat_[A-Za-z0-9_]{50,}`)},
	{"an Anthropic API key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`)},
	{"an OpenAI-style API key", regexp.MustCompile(`\bsk-(proj-|svcacct-)?[A-Za-z0-9_-]{32,}`)},
	{"a Stripe key", regexp.MustCompile(`\b[rs]k_(live|test)_[A-Za-z0-9]{20,}`)},
	{"a Slack token", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`)},
	{"a Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`)},
	{"a JSON web token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	{"a password in a URL", regexp.MustCompile(`\b[a-z][a-z0-9+.-]*://[^\s:/@]+:[^\s@/]{3,}@`)},
}

// assignment finds NAME=value lines whose name says secret. The value is checked separately
// (see looksSecret), so that `API_KEY = process.env.API_KEY` in a code discussion passes.
var assignment = regexp.MustCompile(`(?i)\b[A-Z0-9_]*(SECRET|TOKEN|PASSWORD|PASSWD|API_?KEY|PRIVATE_?KEY|ACCESS_?KEY)[A-Z0-9_]*["']?\s*[:=]\s*["']?([^\s"']+)`)

// Check returns an error naming the kind of secret if any text looks like one.
func Check(texts ...string) error {
	for _, t := range texts {
		for _, p := range patterns {
			if p.re.MatchString(t) {
				return blocked(p.name)
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
// a placeholder: long, token-shaped, and mixing letters with digits.
func looksSecret(v string) bool {
	if len(v) < 12 {
		return false
	}
	var letter, digit bool
	for _, r := range v {
		switch {
		case unicode.IsLetter(r):
			letter = true
		case unicode.IsDigit(r):
			digit = true
		case strings.ContainsRune("-_+/=.~", r):
		default:
			return false // parentheses, $, <, {: code or a placeholder
		}
	}
	return letter && digit
}
