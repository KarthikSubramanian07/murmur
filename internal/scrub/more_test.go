package scrub

import (
	"strings"
	"testing"
)

// Shapes added on 2026-11-09, each named by the error, and a key right after an underscore, which
// \b missed.
func TestBlocksMoreShapes(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"-----BEGIN PGP PRIVATE KEY BLOCK-----", "a private key"},
		{"-----BEGIN RSA PRIVATE KEY-----", "a private key"},
		{`curl -H "Authorization: Bearer ` + "abcDEF123456ghiJKL7890mno" + `" https://api.example.com`, "a bearer token"},
		{"authorization: bearer " + "0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d", "a bearer token"},
		{"post it to https://hooks.slack.com/services/" + "T0123ABCD/B0456EFGH/" + "abcdEFGHijklMNOPqrst1234", "a Slack webhook URL"},
		{"token is ya29." + "a0AfH6SMBx1234567890abcdefghijklmnop", "a Google OAuth token"},
		{"DefaultEndpointsProtocol=https;AccountName=acct;AccountKey=" + strings.Repeat("abcd1234", 11) + "==;EndpointSuffix=core.windows.net", "an Azure storage account key"},
		{"https://bob:" + "pa/ss1234" + "@db.example.com/app", "a password in a URL"},
		{"my_sk-ant-" + "api03-abcdefghijklmnopqrstuvwxyz", "an Anthropic API key"},
		{"key_ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789AB", "a GitHub token"},
		{"creds_AKIA" + "IOSFODNN7EXAMPLE", "an AWS access key"},
		{"DB_PASSWORD=" + "Sup3rS3cretPass!", "a credential assignment"},
		{"PASSWORD='" + "h4ck@th3#planet9" + "'", "a credential assignment"},
	} {
		err := Check(c.text)
		if err == nil {
			t.Errorf("not blocked: %q", c.text)
		} else if !strings.Contains(err.Error(), "looks like "+c.want+".") {
			t.Errorf("%q: want %q: %v", c.text, c.want, err)
		}
	}
}

// Near misses an agent writes in ordinary messages, which the new shapes must let through.
func TestPassesNearMisses(t *testing.T) {
	for _, s := range []string{
		"the API takes a Bearer token in the Authorization header",
		"Authorization: Bearer <token>",
		"Authorization: Bearer $GITHUB_TOKEN",
		"Authorization: Bearer ${TOKEN}",
		"Authorization: Bearer YOUR_ACCESS_TOKEN_HERE",
		"bearer authentication is configured per route",
		"use hooks.slack.com/services/... for the webhook",
		"the hooks.slack.com/services/T000/B000/XXXX example in the docs",
		"Google tokens start with ya29.",
		"AccountKey=<your key>",
		"set AccountKey=YOUR_ACCOUNT_KEY in the connection string",
		"open http://localhost:5173/@vite/client",
		"http://localhost:3000/users/@alice",
		"http://localhost:PORT/@fs/src",
		"git@github.com:a-Fig/murmur.git",
		"-----BEGIN PGP PUBLIC KEY BLOCK-----",
		"https://example.com/cb?token_type=bearer&expires_in=3600&scope=read1",
		"OPENAI_API_KEY=sk-****" + "abcd1234wxyz",
		"set API_KEY=%MY_API_KEY_V2%",
		"PASSWORD=correcthorsebatterystaple", // no digit: as before, a word or a placeholder
		"use_hf_tokenizer_from_pretrained_model_name",
		"risk-" + "abcdefghijklmnopqrstuvwxyz0123456789",
	} {
		if err := Check(s); err != nil {
			t.Errorf("blocked %q: %v", s, err)
		}
	}
}
