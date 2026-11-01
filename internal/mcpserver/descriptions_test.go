package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
)

// What the server tells an agent about messages has to match how they arrive (#57): a message
// usually reaches the recipient as a turn, with a header, subject and body, and a notice with only
// the subject is the fallback. A description that still said the recipient "first sees only your
// subject" would have a sender put the whole message in the subject line, or none of it in the body.
func TestToolsDescribeMessagesAsTurnsFirst(t *testing.T) {
	f := &fakeDaemon{}
	p := repo(t, true, f)
	if _, err := p.RecordConsent(); err != nil {
		t.Fatal(err)
	}
	cs, _ := open(t, p.Top, 25*time.Millisecond)
	waitFor(t, "the tools to be listed", func() bool { return len(toolNamesOf(t, cs)) == len(toolNames) })
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	all := cs.InitializeResult().Instructions
	for _, tool := range res.Tools {
		b, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		all += "\n" + string(b)
		if tool.Name == "send" && !strings.Contains(tool.Description, "as a turn") {
			t.Errorf("the send tool doesn't say a message arrives as a turn: %q", tool.Description)
		}
		if tool.Name == "inbox" && !strings.Contains(tool.Description, "arrived as a turn") {
			t.Errorf("the inbox tool doesn't say a message that arrived as a turn isn't in it: %q", tool.Description)
		}
	}
	for _, stale := range []string{"first sees only", "sees only this until"} {
		if strings.Contains(all, stale) {
			t.Errorf("the server still says %q about messages:\n%s", stale, all)
		}
	}
}

// A joined agent always has the instructions, so they are where it learns to open its user's page
// when asked, long after it read join.md (#117).
func TestInstructionsSayHowToOpenThePage(t *testing.T) {
	if want := "`" + brand.Command + " ui`"; !strings.Contains(Instructions, want) {
		t.Errorf("the instructions don't mention %s:\n%s", want, Instructions)
	}
}
