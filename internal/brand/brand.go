// Package brand holds the tool's working name and every identifier built from it. The product
// name is undecided (docs/VISION.md), so no other package spells the name out: a rename happens
// here, plus the module path.
package brand

// Name is the working name. It reaches the remote in ref names, so once a team has used the
// tool, changing it strands their bus history under the old refs.
const Name = "t4"

const (
	// RefRoot holds every bus ref, on the remote and in each clone's local copy.
	RefRoot = "refs/" + Name + "/"
	// MeshPrefix + deviceID is a device's event log: one commit per event, one writer.
	MeshPrefix = RefRoot + "mesh/"
	// PresencePrefix + deviceID is a device's current state, force-pushed on every change.
	PresencePrefix = RefRoot + "presence/"
	// NamesRef is the registry of session names, one shared ref that devices take names on by
	// compare-and-swap.
	NamesRef = RefRoot + "names"

	// StateDir is the daemon's directory inside the git common dir.
	StateDir = Name
	// ConfigRemote names the remote the bus lives on (default "origin").
	ConfigRemote = Name + ".remote"

	// Command is the binary's name on PATH, as written into teammates' config by init.
	Command = Name
	// MCPServer is the MCP server's name; Claude Code shows its tools as mcp__<name>__<tool>.
	MCPServer = Name

	// JoinDoc and UseDoc are the pages the marker in a repo's AGENTS.md and CLAUDE.md sends an
	// agent to, as raw text: how to join (docs/agents/join.md) and how to use the tools
	// (docs/agents/use.md). They read without login, as the repo is public.
	JoinDoc = "https://raw.githubusercontent.com/a-Fig/t4/main/docs/agents/join.md"
	UseDoc  = "https://raw.githubusercontent.com/a-Fig/t4/main/docs/agents/use.md"

	// InstallSh and InstallPs1 are the one-line installs join.md tells an agent to run. The docs
	// spell them out, so a test holds them to these.
	InstallSh  = "https://raw.githubusercontent.com/a-Fig/t4/main/install.sh"
	InstallPs1 = "https://raw.githubusercontent.com/a-Fig/t4/main/install.ps1"
)
