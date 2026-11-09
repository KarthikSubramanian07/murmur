package bus

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
)

// A presence says which device wrote it, and anyone who can push can write any ref, so on its own
// that is only a claim. The box key a presence carries is what teammates encrypt to (see Box), so
// that claim has to be a fact (#156): each device has an Ed25519 key and seals its presence with
// it, bound to the ref it goes on. Readers pin a device's key the first time they read a sealed
// presence from it (see the daemon's keys.go).
//
// The seal is the last member of the record's JSON object, `"seal":{...}`, and what it signs is the
// object as it was before that member was added, byte for byte. A reader checks the bytes it read,
// never a re-encoding, so a field it doesn't know, written by a newer version, is covered too.
// A daemon from before seals ignores the member, as it ignores any field it doesn't know.
//
// Events are not sealed: Tyler set signing them aside on 2026-11-03 (#155).

// Seal is what a signed record carries: who signed it, with which key, and the signature.
type Seal struct {
	By  string `json:"by"`  // the signing device
	Key string `json:"key"` // its public key, base64
	Sig string `json:"sig"` // base64
}

const sealMember = `"seal":`

// sigContext opens every signed message, so that a signature made for the bus means nothing
// anywhere else. It names no product: a rename must not invalidate what is on the bus.
const sigContext = "agent-bus record v1"

// NewSignKey makes a device's signing key.
func NewSignKey() ed25519.PrivateKey {
	_, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return k
}

// SignPublic is a signing key's public half as seals carry it.
func SignPublic(k ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(k.Public().(ed25519.PublicKey))
}

// signed is the message a seal signs.
func signed(ref, by, key string, body []byte) []byte {
	var b bytes.Buffer
	for _, s := range []string{sigContext, ref, by, key} {
		b.WriteString(s)
		b.WriteByte(0)
	}
	b.Write(body)
	return b.Bytes()
}

// EncodeSealed is Encode with a seal: the record as device by writes it on ref.
func EncodeSealed(v any, subject string, k ed25519.PrivateKey, by, ref string) (string, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	if len(body) < 2 || body[0] != '{' || body[len(body)-1] != '}' {
		return "", errNotObject
	}
	key := SignPublic(k)
	seal, err := json.Marshal(Seal{By: by, Key: key,
		Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(k, signed(ref, by, key, body)))})
	if err != nil {
		return "", err
	}
	sep := ","
	if len(body) == 2 {
		sep = ""
	}
	out := string(body[:len(body)-1]) + sep + sealMember + string(seal) + "}"
	return OneLine(subject, 120) + "\n\n" + out + "\n", nil
}

type sealError string

func (e sealError) Error() string { return string(e) }

const errNotObject = sealError("a record must be a JSON object")

// SealState is what a reader learns from a record's seal.
type SealState int

const (
	// Unsealed: the record carries no seal, as a daemon from before seals writes it.
	Unsealed SealState = iota
	// Sealed: the seal is good, for its By and Key.
	Sealed
	// BadSeal: the record has a seal, and it doesn't hold: not for this ref, not made with its
	// key, or not in the form EncodeSealed writes. Nothing in such a record is trusted.
	BadSeal
)

// OpenSeal checks the seal of a commit message read from ref. With Sealed, the seal is returned.
func OpenSeal(msg, ref string) (SealState, Seal) {
	i := strings.Index(msg, "\n\n")
	if i < 0 {
		return Unsealed, Seal{}
	}
	body := strings.TrimSpace(msg[i+2:])
	var top struct {
		Seal *json.RawMessage `json:"seal"`
	}
	if json.Unmarshal([]byte(body), &top) != nil || top.Seal == nil {
		return Unsealed, Seal{} // unreadable records are the caller's to drop, as before
	}
	var s Seal
	if json.Unmarshal(*top.Seal, &s) != nil {
		return BadSeal, Seal{}
	}
	// The seal must be exactly what EncodeSealed appends, last, so that what precedes it is the
	// signed body. Re-encoding the parsed seal gives the bytes it was written as: its fields are
	// a device ID and base64, which JSON writes one way only.
	enc, err := json.Marshal(s)
	if err != nil {
		return BadSeal, Seal{}
	}
	tail := sealMember + string(enc) + "}"
	if !strings.HasSuffix(body, tail) {
		return BadSeal, Seal{}
	}
	signedBody := strings.TrimSuffix(body, tail)
	switch {
	case strings.HasSuffix(signedBody, ","):
		signedBody = strings.TrimSuffix(signedBody, ",") + "}"
	case signedBody == "{":
		signedBody = "{}"
	default:
		return BadSeal, Seal{}
	}
	pub, err := base64.StdEncoding.DecodeString(s.Key)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return BadSeal, Seal{}
	}
	sig, err := base64.StdEncoding.DecodeString(s.Sig)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return BadSeal, Seal{}
	}
	if !ValidDevice(s.By) || !ed25519.Verify(pub, signed(ref, s.By, s.Key, []byte(signedBody)), sig) {
		return BadSeal, Seal{}
	}
	return Sealed, s
}
