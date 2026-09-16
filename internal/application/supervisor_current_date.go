package application

import "time"

// Use the host clock at request assembly, not the persisted Run creation time
// or dates in old messages. UTC makes the time reference explicit across hosts.
func supervisorCurrentDateContext(now time.Time) string {
	return "Current date from the runtime clock (UTC): " + now.UTC().Format(time.DateOnly) +
		". Resolve relative dates and requests for the latest information against this date. " +
		"This clock value is not evidence that an event occurred; verify dates and claims in the available sources."
}
