package daemon

// SetMaxWholeReply sets maxWholeReply for a test, and returns what puts it back.
func SetMaxWholeReply(n int) func() {
	was := maxWholeReply
	maxWholeReply = n
	return func() { maxWholeReply = was }
}

// IsTurn says prompt is the turn whose text is turn, as the daemon matches them (#611, #651).
func IsTurn(prompt, turn string) bool { return isTurn(prompt, turnStart(turn)) }

// CatchUpWindow is catchUpWindow, how long after the start what is read for a reconnecting session
// is held for it.
const CatchUpWindow = catchUpWindow
