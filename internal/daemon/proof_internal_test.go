package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/proto"
)

// The daemon proves itself to a ping that carries a nonce, without its token, and then takes one
// request on the same connection, which still needs the token (#371).
func TestTheDaemonProvesItselfThenTakesARequest(t *testing.T) {
	for _, c := range []struct {
		name, token string
		ok          bool
	}{{"with the token", "tok", true}, {"without it", "", false}} {
		t.Run(c.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			d := &Daemon{token: "tok"}
			go func() {
				conn, err := ln.Accept()
				if err == nil {
					d.handleConn(context.Background(), conn)
				}
			}()
			conn, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(10 * time.Second))
			r := bufio.NewReader(conn)
			ask := func(req proto.Request) proto.Response {
				t.Helper()
				b, _ := json.Marshal(req)
				conn.Write(append(b, '\n'))
				line, err := r.ReadBytes('\n')
				if err != nil {
					t.Fatalf("%s: %v", req.Op, err)
				}
				var resp proto.Response
				json.Unmarshal(line, &resp)
				return resp
			}
			proof := ask(proto.Request{Op: proto.OpPing, Nonce: "n0nce"})
			if want := proto.Proof("tok", ln.Addr().String(), "n0nce"); proof.Proof != want || proof.Protocol != proto.Protocol {
				t.Fatalf("proof %q protocol %d, want %q %d", proof.Proof, proof.Protocol, want, proto.Protocol)
			}
			resp := ask(proto.Request{Op: proto.OpPing, Token: c.token})
			if resp.OK != c.ok {
				t.Errorf("the request after the proof: %+v", resp)
			}
		})
	}
}

// A proof is of the token, the address and the nonce: change any of them and it changes.
func TestAProofIsOfTheTokenTheAddressAndTheNonce(t *testing.T) {
	p := proto.Proof("tok", "127.0.0.1:1", "n")
	for _, other := range []string{proto.Proof("tok2", "127.0.0.1:1", "n"), proto.Proof("tok", "127.0.0.1:2", "n"), proto.Proof("tok", "127.0.0.1:1", "m")} {
		if other == p {
			t.Errorf("two proofs of different things are both %s", p)
		}
	}
}
