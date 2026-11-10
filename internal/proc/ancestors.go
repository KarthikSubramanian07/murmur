package proc

// Ancestors is pid and the processes above it, nearest first, at most max of them: pid's parent,
// that one's parent, and so on. It stops early where a parent can't be found, and is nil where this
// system has no way to look (see parentOf).
//
// The hook of an agent that gives hooks no process of its own to name (Codex, Grok) sends these, so
// the daemon can find the MCP server the same agent process started: Codex runs a hook through a
// shell, so the agent is its grandparent, not its parent.
func Ancestors(pid, max int) []int {
	var out []int
	seen := map[int]bool{}
	for len(out) < max && pid > 0 && !seen[pid] {
		seen[pid] = true
		out = append(out, pid)
		parent, ok := parentOf(pid)
		if !ok {
			break
		}
		pid = parent
	}
	return out
}
