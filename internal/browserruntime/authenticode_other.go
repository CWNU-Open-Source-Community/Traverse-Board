//go:build !windows

package browserruntime

import "os"

func browserAuthenticodeEvidence(*os.File, string) (AuthenticodeEvidence, error) {
	return AuthenticodeEvidence{
		Source:                 AuthenticodeSourceUnavailable,
		PublisherPolicyVersion: BrowserPublisherPolicyVersion,
	}, nil
}
