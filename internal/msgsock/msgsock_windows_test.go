package msgsock

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Microsoft/go-winio"
)

// On Windows Claude Code's socket is a named pipe, which has its own way to dial.
func TestSendToANamedPipe(t *testing.T) {
	path := fmt.Sprintf(`\\.\pipe\LOCAL\t4-msgsock-test-%d`, os.Getpid())
	ln, err := winio.ListenPipe(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := serveLines(t, ln)
	if err := Send(context.Background(), path, "tok-pipe", "hello through a pipe"); err != nil {
		t.Fatal(err)
	}
	var auth map[string]string
	if err := json.Unmarshal([]byte(f.next(t)), &auth); err != nil || auth["token"] != "tok-pipe" {
		t.Fatalf("auth line: %v %v", auth, err)
	}
	var msg userLine
	if err := json.Unmarshal([]byte(f.next(t)), &msg); err != nil || msg.Message.Content != "hello through a pipe" {
		t.Fatalf("user line: %+v %v", msg, err)
	}
}

// A pipe nobody serves fails without giving away its name or the token.
func TestSendToAMissingPipeHidesTheName(t *testing.T) {
	path := `\\.\pipe\LOCAL\t4-msgsock-no-such-pipe-77c1`
	err := Send(context.Background(), path, "tok-secret-77c1", "x")
	if err == nil {
		t.Fatal("sending to nothing worked")
	}
	for _, secret := range []string{path, "no-such-pipe-77c1", "tok-secret-77c1"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the error gives away %q: %v", secret, err)
		}
	}
}
