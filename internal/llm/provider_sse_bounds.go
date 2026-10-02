package llm

const (
	maxProviderSSEEventBytes = maxOpenAIStreamEventBytes
	maxProviderSSEDataLines  = 4096
)

// Count the actual joined payload, including the separator before every data
// line after the first. The line cap also bounds empty-line slice entries.
type providerSSEEventSize struct {
	bytes int
	lines int
}

func (size *providerSSEEventSize) append(partBytes int) bool {
	if partBytes < 0 || partBytes > maxProviderSSEEventBytes ||
		size.bytes < 0 || size.bytes > maxProviderSSEEventBytes ||
		size.lines < 0 || size.lines >= maxProviderSSEDataLines {
		return false
	}
	additional := partBytes
	if size.lines > 0 {
		additional++
	}
	// Both operands are bounded before subtraction/addition, so even an
	// invalid oversized input cannot wrap the counter into an accepted value.
	if additional > maxProviderSSEEventBytes-size.bytes {
		return false
	}
	size.bytes += additional
	size.lines++
	return true
}
