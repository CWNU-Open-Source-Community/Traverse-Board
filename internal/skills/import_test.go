package skills

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBuildPackageFromDirRoundTrip(t *testing.T) {
	dir := t.TempDir()
	content := []byte("# Directory import\n\nReview bounded evidence.\n")
	manifest := fixtureManifest(content)
	manifest.Name = "dir-import"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := BuildPackageFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePackage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Preview().Manifest.Name != "dir-import" {
		t.Fatalf("unexpected manifest: %#v", parsed.Preview().Manifest)
	}
}

func TestBuildPackageFromDirSigned(t *testing.T) {
	dir := t.TempDir()
	content := []byte("# Signed dir\n")
	manifest := fixtureManifest(content)
	manifest.Name = "signed-dir"
	manifest.Publisher = "ctf-blue-team"
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := SignPackage(manifest, content, privateKey, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSignedPackage(raw)
	if err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	signatureRaw, err := json.Marshal(parsed.Signature)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string][]byte{
		"SKILL.md": content, "manifest.json": manifestRaw, "SIGNATURE.json": signatureRaw,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), value, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	built, err := BuildPackageFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSignedPackage(built); err != nil {
		t.Fatalf("signed directory import failed to verify: %v", err)
	}
}

func TestBuildPackageFromDirRejectsHostileLayouts(t *testing.T) {
	// Symlink/junction: skip when the platform cannot create one.
	dir := t.TempDir()
	content := []byte("# Hostile\n")
	manifest := fixtureManifest(content)
	manifest.Name = "hostile"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "SIGNATURE.json")
	if err := os.Symlink(filepath.Join(dir, "SKILL.md"), link); err == nil {
		if _, err := BuildPackageFromDir(dir); err == nil {
			t.Fatal("directory with symlink was imported")
		}
	} else if runtime.GOOS != "windows" {
		t.Fatalf("symlink creation failed unexpectedly: %v", err)
	}
	_ = os.Remove(link)
	// Subdirectory.
	sub := filepath.Join(dir, "nested")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildPackageFromDir(dir); err == nil {
		t.Fatal("directory with subdirectory was imported")
	}
	_ = os.Remove(sub)
	// Extra file.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildPackageFromDir(dir); err == nil {
		t.Fatal("directory with extra file was imported")
	}
	_ = os.Remove(filepath.Join(dir, "README.md"))
	// Missing manifest.
	if err := os.Remove(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildPackageFromDir(dir); err == nil {
		t.Fatal("directory without manifest was imported")
	}
}

func TestBuildPackageFromDirRejectsBadUTF8Content(t *testing.T) {
	dir := t.TempDir()
	content := []byte{0xff, 0xfe, 0x00, 0x41}
	manifest := fixtureManifest([]byte("# placeholder\n"))
	manifest.Name = "bad-utf8"
	// Manifest must still describe the hostile bytes as its content.
	digest := sha256.Sum256(content)
	manifest.ContentSHA256 = hex.EncodeToString(digest[:])
	manifest.ContentBytes = len(content)
	manifest.ContentTokenUpperBound = ContentTokenUpperBound(content)
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildPackageFromDir(dir); err == nil {
		t.Fatal("directory with non-UTF-8 content was imported")
	}
}

func TestFetchPinnedURLEnforcesHTTPSHashAndRedirects(t *testing.T) {
	payload := []byte("package-bytes")
	digest := sha256.Sum256(payload)
	pin := hex.EncodeToString(digest[:])
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/redirect-cross-host":
			http.Redirect(writer, request, "https://other.invalid/pkg.zip", http.StatusFound)
		case "/redirect-insecure":
			http.Redirect(writer, request, "http://"+request.Host+"/pkg.zip", http.StatusFound)
		case "/redirect-same-host":
			http.Redirect(writer, request, "/pkg.zip", http.StatusFound)
		case "/missing":
			http.NotFound(writer, request)
		case "/oversize":
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte(strings.Repeat("a", MaxURLImportBytes+1)))
		default:
			_, _ = writer.Write(payload)
		}
	}))
	defer server.Close()
	client := server.Client()
	ctx := context.Background()
	// Exact pin succeeds.
	got, err := FetchPinnedURL(ctx, server.URL+"/pkg.zip", pin, client)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("pinned fetch failed: %v", err)
	}
	// Wrong pin fails.
	if _, err := FetchPinnedURL(ctx, server.URL+"/pkg.zip", strings.Repeat("0", 64), client); err == nil {
		t.Fatal("mismatched SHA-256 pin was accepted")
	}
	// Cross-host redirect fails.
	if _, err := FetchPinnedURL(ctx, server.URL+"/redirect-cross-host", pin, client); err == nil {
		t.Fatal("cross-host redirect was followed")
	}
	// HTTPS→HTTP redirect fails.
	if _, err := FetchPinnedURL(ctx, server.URL+"/redirect-insecure", pin, client); err == nil {
		t.Fatal("insecure redirect was followed")
	}
	// Same-host HTTPS redirect with the pinned bytes succeeds.
	if _, err := FetchPinnedURL(ctx, server.URL+"/redirect-same-host", pin, client); err != nil {
		t.Fatalf("same-host redirect failed: %v", err)
	}
	// Non-200 fails.
	if _, err := FetchPinnedURL(ctx, server.URL+"/missing", pin, client); err == nil {
		t.Fatal("404 import succeeded")
	}
	// Oversized body fails.
	if _, err := FetchPinnedURL(ctx, server.URL+"/oversize", strings.Repeat("0", 64), client); err == nil {
		t.Fatal("oversized body was accepted")
	}
	// Non-HTTPS URL fails up front.
	if _, err := FetchPinnedURL(ctx, "http://example.com/pkg.zip", pin, client); err == nil {
		t.Fatal("plain HTTP URL was accepted")
	}
}

func TestFetchGitCommitStagesExactCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary is not available")
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "--quiet")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "test")
	runGit(t, repo, "config", "commit.gpgsign", "false")
	runGit(t, repo, "config", "core.autocrlf", "false")
	content := []byte("# Git skill\n")
	manifest := fixtureManifest(content)
	manifest.Name = "git-import"
	if err := os.WriteFile(filepath.Join(repo, "SKILL.md"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "manifest.json"), manifestRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "manifest.json", "SKILL.md")
	runGit(t, repo, "commit", "--quiet", "-m", "skill")
	head := strings.TrimSpace(runGitOutput(t, repo, "rev-parse", "HEAD"))
	staging := filepath.Join(t.TempDir(), "staging")
	fileURL := "file://" + filepath.ToSlash(repo)
	if err := stageGitCommit(context.Background(), fileURL, head, staging); err != nil {
		t.Fatalf("stage pinned commit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "manifest.json")); err != nil {
		t.Fatalf("staged manifest missing: %v", err)
	}
	built, err := BuildPackageFromDir(staging)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePackage(built); err != nil {
		t.Fatalf("staged git import did not parse: %v", err)
	}
	// Unknown commit fails.
	if err := stageGitCommit(context.Background(), fileURL, strings.Repeat("0", 40), filepath.Join(t.TempDir(), "bad")); err == nil {
		t.Fatal("unknown commit was staged")
	}
}

func TestFetchGitCommitRejectsInsecureInputs(t *testing.T) {
	ctx := context.Background()
	if err := FetchGitCommit(ctx, "http://example.com/repo.git", strings.Repeat("a", 40), t.TempDir()); err == nil {
		t.Fatal("plain HTTP git URL was accepted")
	}
	if err := FetchGitCommit(ctx, "https://example.com/repo.git", "abcd", t.TempDir()); err == nil {
		t.Fatal("short commit SHA was accepted")
	}
	if err := FetchGitCommit(ctx, "https://example.com/repo.git", strings.Repeat("A", 40), t.TempDir()); err == nil {
		t.Fatal("uppercase commit SHA was accepted")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(string(output)))
	}
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(output)
}
