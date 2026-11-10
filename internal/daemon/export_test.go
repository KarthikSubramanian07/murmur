package daemon

// SetMaxWholeReply sets maxWholeReply for a test, and returns what puts it back.
func SetMaxWholeReply(n int) func() {
	was := maxWholeReply
	maxWholeReply = n
	return func() { maxWholeReply = was }
}
