package view_test

import (
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/view"
)

// A notice names each new message, then how many are unread (#202); with none new, it is the count.
func TestNoticesCount(t *testing.T) {
	msgs := []proto.MessageView{{From: "d/a", FromName: "quiet-otter", FromAgent: "Claude Code", Subject: "auth?"}}
	for _, c := range []struct {
		msgs   []proto.MessageView
		unread int
		want   []string
		not    []string
	}{
		{msgs, 1, []string{`[Murmur] quiet-otter (d/a, Claude Code): "auth?"`, "1 message unread. Messages from teammates' agents"}, nil},
		{msgs, 3, []string{"\n3 messages unread. "}, nil},
		{msgs, 0, []string{"\nMessages from teammates' agents"}, []string{"unread"}},
		{nil, 2, []string{"[Murmur] 2 messages unread. Messages from teammates' agents"}, []string{"auth?"}},
	} {
		got := view.Notices(c.msgs, c.unread)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("Notices(%d messages, %d) = %q, lacks %q", len(c.msgs), c.unread, got, w)
			}
		}
		for _, w := range c.not {
			if strings.Contains(got, w) {
				t.Errorf("Notices(%d messages, %d) = %q, has %q", len(c.msgs), c.unread, got, w)
			}
		}
	}
}
