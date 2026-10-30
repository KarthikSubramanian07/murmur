package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestSigned(t *testing.T) {
	body := []byte(`{"action":"queued"}`)
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	cases := []struct {
		name, secret, sig string
		body              []byte
		want              bool
	}{
		{"right secret", "s3cret", good, body, true},
		{"wrong secret", "other", good, body, false},
		{"changed body", "s3cret", good, []byte(`{"action":"completed"}`), false},
		{"no prefix", "s3cret", good[len("sha256="):], body, false},
		{"empty", "s3cret", "", body, false},
		{"not hex", "s3cret", "sha256=zz", body, false},
	}
	for _, c := range cases {
		if got := signed(c.secret, c.body, c.sig); got != c.want {
			t.Errorf("%s: signed = %v, want %v", c.name, got, c.want)
		}
	}
}
