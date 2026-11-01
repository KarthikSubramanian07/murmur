package mcpserver

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/a-Fig/murmur/internal/client"
	"github.com/a-Fig/murmur/internal/proto"
)

// Muse, Pi and Grok give the server its session's own ID in the environment, and the hello carries
// it, with an ID made from it; what Claude Code put in an environment they passed on isn't theirs.
func TestHelloTakesTheSessionFromTheAgentsVariable(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "0a1b2c3d-aaaa-bbbb-cccc-ddddeeeeffff") // a Claude Code session's, inherited
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/claude.sock")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "tok")
	t.Setenv("T3CODE_CLI_PATH", "/t3/bin/t3")
	for agent, v := range nativeEnv {
		t.Run(agent, func(t *testing.T) {
			t.Setenv(v, "01a190f5-a509-7000-bd17-2120db057488")
			h := helloFor(agent, "/work")
			if h.Native != "01a190f5-a509-7000-bd17-2120db057488" || h.ID != nativeID(h.Native) || len(h.ID) != 6 {
				t.Errorf("hello: %+v", h)
			}
			if h.ClaudeSession != "" || h.MsgSocket != "" || h.MsgToken != "" {
				t.Errorf("took Claude Code's variables: %+v", h)
			}
			if h.Agent != agent+" in T3" || h.T3CLI != "/t3/bin/t3" {
				t.Errorf("agent %q, route %+v", h.Agent, h.Route)
			}
		})
	}
	// Two IDs made in the same second, as Muse's and OpenCode's begin with the time, get different
	// Murmur IDs.
	if nativeID("01a190f5-a509-7000-bd17-2120db057488") == nativeID("01a190f5-a50a-7000-8000-000000000000") {
		t.Error("two sessions share an ID")
	}
}

func openCodeCall(session string) *mcp.CallToolRequest {
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "who"}}
	if session != "" {
		req.Params.SetMeta(map[string]any{openCodeSessionMeta: session})
	}
	return req
}

// One OpenCode server serves every session in its directory: each session that calls is
// registered on its own, by the ID the call names, and found in T3 by it.
func TestASharedServerRegistersEachSessionThatCalls(t *testing.T) {
	fastSeek(t)
	t.Setenv("T3CODE_CLI_PATH", "/t3/bin/t3")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "0a1b2c3d-aaaa-bbbb-cccc-ddddeeeeffff")
	f := &fakeDaemon{}
	p := repo(t, true, f)
	if _, err := p.RecordConsent(); err != nil {
		t.Fatal(err)
	}
	finds := &finder{threads: map[string]string{"ses_aaa": "thread-a", "ses_bbb": "thread-b"}}
	old := threadOf
	threadOf = func(ctx context.Context, _, _, native string) (string, error) { return finds.find(ctx, native) }
	t.Cleanup(func() { threadOf = old })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &server{opt: Options{Agent: "opencode"}, cwd: "/work", place: p, c: &client.Client{GitDir: p.GitDir},
		links: map[string]*sessionLink{}, lctx: ctx}
	if f.helloCount() != 0 {
		t.Fatal("registered before any call")
	}
	for _, ses := range []string{"ses_aaa", "ses_bbb", "ses_aaa"} {
		if _, err := s.callFrom(ctx, openCodeCall(ses), proto.Request{Op: proto.OpWho}); err != nil {
			t.Fatalf("%s: %v", ses, err)
		}
	}
	if n := len(s.links); n != 2 {
		t.Errorf("%d sessions, want 2", n)
	}
	threads := map[string]string{}
	f.mu.Lock()
	hellos := append([]proto.Hello(nil), f.hellos...)
	f.mu.Unlock()
	for _, h := range hellos {
		threads[h.Native] = h.T3Thread
		if h.ClaudeSession != "" || h.ID != nativeID(h.Native) || h.Agent != "opencode in T3" {
			t.Errorf("hello: %+v", h)
		}
	}
	if threads["ses_aaa"] != "thread-a" || threads["ses_bbb"] != "thread-b" {
		t.Errorf("threads: %v", threads)
	}

	// A session quiet for longer than sharedIdle is dropped at the next call of another.
	old2 := sharedIdle
	sharedIdle = 0
	t.Cleanup(func() { sharedIdle = old2 })
	time.Sleep(10 * time.Millisecond)
	if _, err := s.callFrom(ctx, openCodeCall("ses_bbb"), proto.Request{Op: proto.OpWho}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.links["ses_aaa"]; ok || len(s.links) != 1 {
		t.Errorf("sessions left: %v", s.links)
	}

	// A call that names no session (an older OpenCode) is one session for the whole process.
	if _, err := s.callFrom(ctx, openCodeCall(""), proto.Request{Op: proto.OpWho}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.links[""]; !ok {
		t.Error("no session for the calls that name none")
	}
}

// Off, a shared server registers nobody.
func TestASharedServerOffRegistersNobody(t *testing.T) {
	s := &server{opt: Options{Agent: "opencode"}}
	if _, err := s.callFrom(context.Background(), openCodeCall("ses_aaa"), proto.Request{Op: proto.OpWho}); err != errPassive {
		t.Errorf("err %v", err)
	}
}
