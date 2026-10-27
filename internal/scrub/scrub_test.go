package scrub

import "testing"

func TestBlocks(t *testing.T) {
	for _, s := range []string{
		"key is AKIAIOSFODNN7EXAMPLE ok",
		"use ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789AB",
		"ANTHROPIC_API_KEY=sk-ant-" + "api03-abcdefghijklmnopqrstuvwxyz",
		"stripe sk_live_" + "abcdefghijklmnopqrstuvwx",
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"DATABASE_URL=postgres://app:hunter2pass@db.example.com/app",
		"STRIPE_SECRET=abc123def456ghi789",
		`"password": "Tr0ub4dor3xyz1"`,
	} {
		if Check(s) == nil {
			t.Errorf("not blocked: %q", s)
		}
	}
}

func TestPasses(t *testing.T) {
	for _, s := range []string{
		"Are you changing the auth middleware?",
		"API_KEY = process.env.API_KEY",
		"set STRIPE_SECRET_KEY in your .env, not here",
		"TOKEN=<your token>",
		"PASSWORD=${DB_PASSWORD}",
		"see https://github.com/a-Fig/t4/pull/7",
		"the sk- prefix is for keys",
	} {
		if err := Check(s); err != nil {
			t.Errorf("blocked %q: %v", s, err)
		}
	}
}
