package daemon

// SetMaxWholeReply sets maxWholeReply for a test, and returns what puts it back.
func SetMaxWholeReply(n int) func() {
	was := maxWholeReply
	maxWholeReply = n
	return func() { maxWholeReply = was }
}

// Holds is holds, for a test of how a prompt is matched to a turn (#611).
func Holds(prompt, subject string) bool { return holds(prompt, subject) }

// CatchUpWindow is catchUpWindow, how long after the start what is read for a reconnecting session
// is held for it.
const CatchUpWindow = catchUpWindow
