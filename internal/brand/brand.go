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
	// ClaimsRef is shared by every device and used as a compare-and-swap.
	ClaimsRef = RefRoot + "claims"

	// StateDir is the daemon's directory inside the git common dir.
	StateDir = Name
	// ConfigRemote names the remote the bus lives on (default "origin").
	ConfigRemote = Name + ".remote"

	// Command is the binary's name on PATH, as written into teammates' config by init.
	Command = Name
	// MCPServer is the MCP server's name; Claude Code shows its tools as mcp__<name>__<tool>.
	MCPServer = Name
)
