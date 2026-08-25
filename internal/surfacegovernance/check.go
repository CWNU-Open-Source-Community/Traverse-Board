package surfacegovernance

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

var requiredPullRequestTemplateFields = []string{
	"## Surface governance",
	"No Surface is added, promoted, downgraded, deprecated, or removed",
	"Registry item(s)",
	"Target tier / transition",
	"Entry criteria / decision",
	"Owner",
	"Shared Go Application contract",
	"Authority impact",
	"Supported platforms",
	"Release / test evidence",
	"Compatibility strategy",
	"Deprecation window",
	"Removal / rollback plan",
}

func CheckDocument(path string, generated []byte) error {
	committed, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(committed, generated) {
		return fmt.Errorf("generated Surface inventory drifted from the registry; run `go run ./cmd/surfacecheck -write`")
	}
	return nil
}

func ValidatePullRequestTemplate(content []byte) error {
	template := string(content)
	var missing []string
	for _, field := range requiredPullRequestTemplateFields {
		if !strings.Contains(template, field) {
			missing = append(missing, field)
		}
	}
	if len(missing) != 0 {
		return fmt.Errorf("pull request template is missing Surface declarations: %s",
			strings.Join(missing, ", "))
	}
	return nil
}
