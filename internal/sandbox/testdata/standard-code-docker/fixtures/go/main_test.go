package fixture

import (
	"errors"
	"io/fs"
	"os"
	"testing"
)

func TestOfflineBoundaryAndWritableWorkspace(t *testing.T) {
	for _, name := range []string{"HOME", "SSH_AUTH_SOCK", "DOCKER_HOST",
		"GIT_ASKPASS", "AWS_ACCESS_KEY_ID"} {
		if os.Getenv(name) != "" {
			t.Fatalf("host credential environment leaked through %s", name)
		}
	}
	for _, name := range []string{"/var/run/docker.sock", "/root/.ssh", "/host"} {
		if _, err := os.Stat(name); err == nil {
			t.Fatalf("undeclared host path is visible: %s (%v)", name, err)
		} else if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("undeclared host path check failed: %s (%v)", name, err)
		}
	}
	if err := os.WriteFile("go-output.txt", []byte("go offline fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
