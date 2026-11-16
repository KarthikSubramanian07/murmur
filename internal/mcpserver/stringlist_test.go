package mcpserver

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/a-Fig/murmur/internal/proto"
)

func (f *fakeDaemon) requestsOf(op string) []proto.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []proto.Request
	for _, r := range f.requests {
		if r.Op == op {
			out = append(out, r)
		}
	}
	return out
}

func callListTool(t *testing.T, f *fakeDaemon, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	p := repo(t, true, f)
	if _, err := p.RecordConsent(); err != nil {
		t.Fatal(err)
	}
	cs, _ := open(t, p.Top, time.Hour)
	waitFor(t, "the session to register", func() bool { return f.helloCount() == 1 })
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// A Muse harness sends a list argument as a string holding the JSON array (#698), which the
// SDK's schema validation used to refuse before the handler ran. The list fields of tags, who
// and send take that string, a comma-separated string, or a real array alike.
func TestTagsListSentAsAString(t *testing.T) {
	for _, c := range []struct {
		name string
		add  any
		want []string
	}{
		{"a JSON array in a string", `["bees", "group:worker-bees"]`, []string{"bees", "group:worker-bees"}},
		{"a comma-separated string", "bees, group:worker-bees", []string{"bees", "group:worker-bees"}},
		{"one tag as a plain string", "bees", []string{"bees"}},
		{"a real array", []string{"bees"}, []string{"bees"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeDaemon{}
			res := callListTool(t, f, "tags", map[string]any{"add": c.add})
			if res.IsError {
				t.Fatalf("tags with add as %s is an error: %+v", c.name, res.Content[0].(*mcp.TextContent).Text)
			}
			got := f.requestsOf(proto.OpTags)
			if len(got) != 1 || !slices.Equal(got[0].Add, c.want) {
				t.Errorf("the daemon was asked for %+v, want one tags request adding %q", got, c.want)
			}
		})
	}
}

func TestSendFilesSentAsAString(t *testing.T) {
	f := &fakeDaemon{}
	path := filepath.Join(t.TempDir(), "note.txt")
	res := callListTool(t, f, "send", map[string]any{
		"to": "quiet-otter", "subject": "a file", "files": fmt.Sprintf("[%q]", path)})
	if res.IsError {
		t.Fatalf("send with files as a string is an error: %+v", res.Content[0].(*mcp.TextContent).Text)
	}
	got := f.requestsOf(proto.OpSend)
	if len(got) != 1 || !slices.Equal(got[0].Files, []string{path}) {
		t.Errorf("the daemon was asked for %+v, want one send carrying [%s]", got, path)
	}
}

func TestWhoTagsSentAsAString(t *testing.T) {
	f := &fakeDaemon{}
	res := callListTool(t, f, "who", map[string]any{"tags": `["bees"]`})
	if res.IsError {
		t.Fatalf("who with tags as a string is an error: %+v", res.Content[0].(*mcp.TextContent).Text)
	}
	got := f.requestsOf(proto.OpWho)
	if len(got) != 1 || !slices.Equal(got[0].Tags, []string{"bees"}) {
		t.Errorf("the daemon was asked for %+v, want one who request for [bees]", got)
	}
}
