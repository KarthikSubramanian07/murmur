package view

import (
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/proto"
)

// A person reads none of the conversation they wrote into, so a turn from one says so whether or not
// the agent has met them, and the reply isn't optional.
func TestTurnFromAPerson(t *testing.T) {
	name := brand.Product
	m := proto.MessageView{From: "dev-0001/" + bus.PersonSession, FromName: "ada", FromAgent: bus.AgentPerson, FromOwner: "smash", To: "x", Subject: "look at this", Body: "please"}
	last := "ada sees only what you `send`, not this conversation. Answer with " + name + "'s `send` to ada.\n"
	want := "Message from ada (a person, on " + name + "'s page, on smash's machine) via " + name + ":\n\nlook at this\n\nplease\n\n" + last
	if got := Turn(m); got != want {
		t.Errorf("first turn:\n%s\nwant:\n%s", got, want)
	}
	m.Introduced = true
	want = "Message from ada via " + name + ":\n\nlook at this\n\nplease\n\n" + last
	if got := Turn(m); got != want {
		t.Errorf("later turn:\n%s\nwant:\n%s", got, want)
	}
}

func TestPeopleAreNotCalledAgents(t *testing.T) {
	agent := proto.MessageView{From: "dev-0001/abc123", FromName: "quiet-otter", FromAgent: "Codex", Subject: "a"}
	person := proto.MessageView{From: "dev-0001/" + bus.PersonSession, FromName: "ada", FromAgent: bus.AgentPerson, Subject: "b", Body: "c"}
	if got := Notices([]proto.MessageView{agent}, 1); !strings.Contains(got, "from teammates' agents") {
		t.Errorf("an agent's notice: %s", got)
	}
	if got := Notices([]proto.MessageView{person}, 1); !strings.Contains(got, "from teammates and their agents") {
		t.Errorf("a person's notice: %s", got)
	}
	got := Inbox(proto.Response{Messages: []proto.MessageView{person}})
	for _, want := range []string{"from teammates and their agents", "ada is a person, and sees only what you send them. Reply to ada."} {
		if !strings.Contains(got, want) {
			t.Errorf("the inbox lacks %q:\n%s", want, got)
		}
	}
}
