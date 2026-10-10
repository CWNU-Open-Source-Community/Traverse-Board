package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/sbxmcp"
)

const sbxTestMCPHelperName = "traverse-empty-0123456789abcdef0123456789abcdef"

func sbxTestMCPMetadata(executable string) map[string]map[string]any {
	request := map[string]any{
		"Name": sbxTestMCPHelperName, "Command": executable, "Args": []string{sbxmcp.Arg}, "Cwd": filepath.Dir(executable),
		"URL": "", "Local": false, "Env": nil, "EnvOverride": nil, "SecretEnv": nil, "Headers": nil,
		"OAuthOverride": nil, "OAuthResource": "", "PathOverride": "", "PortOverride": 0,
		"TransportOverride": "", "DisableHTTP2": false, "SkipSSRFCheck": false,
	}
	spec := map[string]any{
		"Name": sbxTestMCPHelperName, "Type": "local", "Command": []string{executable, sbxmcp.Arg},
		"ResolvedCommand": executable, "Cwd": filepath.Dir(executable), "URL": "", "RegistryURL": "", "Image": "",
		"Env": nil, "EnvOverride": nil, "SecretEnv": nil, "Headers": nil, "RequiresOAuth": false,
		"OAuthOverride": nil, "OAuthProviders": nil, "OAuthRequestedScopes": nil, "OAuthResourceOverride": "",
		"CallbackPort": 0, "HTTPPath": "", "HTTPPort": 0, "HTTPTransport": "", "RemoteTransport": "",
		"DisableHTTP2": false, "SSRFCheckFailed": false, "SSRFCheckReason": "",
	}
	return map[string]map[string]any{"request": request, "spec": spec}
}

func sbxTestMCPMetadataBytes(t *testing.T, metadata any) []byte {
	t.Helper()
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSBXMCPStoredRegistrationBindsTheCompleteHostInvocation(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "product-helper.exe")
	metadata := sbxTestMCPMetadata(executable)
	if err := sbxValidateMCPRegistration(sbxTestMCPMetadataBytes(t, metadata), sbxTestMCPHelperName, executable, sbxmcp.Arg); err != nil {
		t.Fatalf("exact stored command was refused: %v", err)
	}
	// Empty collections are harmless even if their serialization differs from
	// the real v0.47 record's nulls. Nonempty collections never grant readiness.
	for _, section := range []string{"request", "spec"} {
		metadata[section]["Env"] = map[string]string{}
		metadata[section]["EnvOverride"] = []string{}
		metadata[section]["SecretEnv"] = map[string]string{}
		metadata[section]["Headers"] = []string{}
		metadata[section]["OAuthOverride"] = map[string]string{}
	}
	if err := sbxValidateMCPRegistration(sbxTestMCPMetadataBytes(t, metadata), sbxTestMCPHelperName, executable, sbxmcp.Arg); err != nil {
		t.Fatalf("empty collections were refused: %v", err)
	}
}

func TestSBXMCPStoredRegistrationRejectsHiddenEnvironmentAndCredentialChanges(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "product-helper.exe")
	for _, section := range []string{"request", "spec"} {
		for _, field := range []string{"Env", "EnvOverride", "SecretEnv", "Headers", "OAuthOverride"} {
			t.Run(section+"/"+field, func(t *testing.T) {
				metadata := sbxTestMCPMetadata(executable)
				metadata[section][field] = map[string]string{"SYNTHETIC_BINDING": "must-not-launch"}
				if err := sbxValidateMCPRegistration(sbxTestMCPMetadataBytes(t, metadata), sbxTestMCPHelperName, executable, sbxmcp.Arg); !errors.Is(err, ErrSBXBoundary) {
					t.Fatal("hidden launch or credential configuration was accepted")
				}
			})
		}
	}
}

func TestSBXMCPStoredRegistrationRejectsCommandDirectoryAndTransportDrift(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "product-helper.exe")
	for _, change := range []struct {
		section, field string
		value          any
	}{
		{"request", "Command", "different.exe"}, {"request", "Args", []string{sbxmcp.Arg, "extra"}},
		{"request", "Cwd", filepath.Dir(filepath.Dir(executable))}, {"request", "Name", "other-helper"},
		{"request", "URL", "https://unwanted.invalid"}, {"request", "Local", true},
		{"request", "OAuthResource", "unwanted"}, {"request", "PathOverride", "/mcp"},
		{"request", "PortOverride", 443}, {"request", "TransportOverride", "http"},
		{"request", "DisableHTTP2", true}, {"request", "SkipSSRFCheck", true},
		{"spec", "Command", []string{executable}}, {"spec", "ResolvedCommand", "different.exe"},
		{"spec", "Cwd", filepath.Dir(filepath.Dir(executable))}, {"spec", "Name", "other-helper"},
		{"spec", "Type", "remote"}, {"spec", "RequiresOAuth", true},
		{"spec", "URL", "https://unwanted.invalid"}, {"spec", "RegistryURL", "unwanted"},
		{"spec", "Image", "unwanted"}, {"spec", "OAuthProviders", []string{"unwanted"}},
		{"spec", "OAuthRequestedScopes", []string{"unwanted"}}, {"spec", "OAuthResourceOverride", "unwanted"},
		{"spec", "CallbackPort", 443}, {"spec", "HTTPPath", "/mcp"}, {"spec", "HTTPPort", 443},
		{"spec", "HTTPTransport", "http"}, {"spec", "RemoteTransport", "http"},
		{"spec", "DisableHTTP2", true}, {"spec", "SSRFCheckFailed", true}, {"spec", "SSRFCheckReason", "unwanted"},
	} {
		t.Run(change.section+"/"+change.field, func(t *testing.T) {
			metadata := sbxTestMCPMetadata(executable)
			metadata[change.section][change.field] = change.value
			if err := sbxValidateMCPRegistration(sbxTestMCPMetadataBytes(t, metadata), sbxTestMCPHelperName, executable, sbxmcp.Arg); !errors.Is(err, ErrSBXBoundary) {
				t.Fatal("stored host invocation drift was accepted")
			}
		})
	}
}

func TestSBXMCPStoredRegistrationRejectsIncompleteAmbiguousAndExpandedSchemas(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "product-helper.exe")
	for _, section := range []string{"request", "spec"} {
		for _, change := range []string{"missing-env", "case-alias", "extra-field", "null-directory"} {
			t.Run(section+"/"+change, func(t *testing.T) {
				metadata := sbxTestMCPMetadata(executable)
				switch change {
				case "missing-env":
					delete(metadata[section], "Env")
				case "case-alias":
					metadata[section]["cwd"] = metadata[section]["Cwd"]
					delete(metadata[section], "Cwd")
				case "extra-field":
					metadata[section]["UnreviewedLaunchOption"] = "unwanted"
				case "null-directory":
					metadata[section]["Cwd"] = nil
				}
				if err := sbxValidateMCPRegistration(sbxTestMCPMetadataBytes(t, metadata), sbxTestMCPHelperName, executable, sbxmcp.Arg); !errors.Is(err, ErrSBXBoundary) {
					t.Fatal("incomplete or ambiguous metadata was accepted")
				}
			})
		}
	}
	data := sbxTestMCPMetadataBytes(t, sbxTestMCPMetadata(executable))
	for _, malformed := range [][]byte{
		[]byte(strings.Replace(string(data), `"Env":null`, `"Env":null,"Env":{"BAD":"unwanted"}`, 1)),
		append(append([]byte{}, data...), []byte(" {}")...),
		[]byte("null"), []byte("[]"), []byte("{}"), []byte(`{"request":null,"spec":null}`),
		[]byte(strings.Repeat(" ", sbxMCPRegistrationLimit+1)),
	} {
		if err := sbxValidateMCPRegistration(malformed, sbxTestMCPHelperName, executable, sbxmcp.Arg); !errors.Is(err, ErrSBXBoundary) {
			t.Fatal("malformed metadata was accepted")
		}
	}
}

func TestSBXMCPStoredMetadataReaderRejectsAliasesLinksAndOversizeFiles(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "owned-registration.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := sbxReadMCPRegistration(t.Context(), path); err != nil || string(data) != "{}" {
		t.Fatalf("ordinary metadata read failed: %v", err)
	}
	link := filepath.Join(root, "linked-registration.json")
	if err := os.Link(path, link); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, link} {
		if _, err := sbxReadMCPRegistration(t.Context(), candidate); !errors.Is(err, ErrSBXBoundary) {
			t.Fatal("multiply-linked metadata was read")
		}
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", sbxMCPRegistrationLimit)), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := sbxReadMCPRegistration(t.Context(), path); err != nil || len(data) != sbxMCPRegistrationLimit {
		t.Fatalf("metadata at the size limit was refused: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", sbxMCPRegistrationLimit+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := sbxReadMCPRegistration(t.Context(), path); !errors.Is(err, ErrSBXBoundary) {
		t.Fatal("oversize metadata was read")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sbxReadMCPRegistration(ctx, path); !errors.Is(err, ErrSBXBoundary) {
		t.Fatal("cancelled read was accepted")
	}
	if _, err := sbxReadMCPRegistration(t.Context(), root); !errors.Is(err, ErrSBXBoundary) {
		t.Fatal("directory metadata was accepted")
	}
}

func TestSBXMCPStoredMetadataReaderDistinguishesVerifiedAbsence(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(root, "absent.json"),
		filepath.Join(root, "not-yet-created", "servers", "absent.json"),
	} {
		if data, err := sbxReadMCPRegistration(t.Context(), path); data != nil || !errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrSBXBoundary) {
			t.Fatal("verified missing registration was not distinguished from an invalid boundary")
		}
	}
	ordinaryFile := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(ordinaryFile, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := sbxReadMCPRegistration(t.Context(), filepath.Join(ordinaryFile, "absent.json")); !errors.Is(err, ErrSBXBoundary) || errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid parent was classified as a missing registration")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := sbxReadMCPRegistration(ctx, filepath.Join(root, "absent.json")); !errors.Is(err, ErrSBXBoundary) || errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled verification authorized registration")
	}
}

func TestSBXMCPStoredMetadataReaderNeverTreatsDanglingLinksAsMissing(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "dangling.json")
	if err := os.Symlink(filepath.Join(root, "absent-target.json"), link); err != nil {
		t.Skip("creating symlinks is unavailable on this host")
	}
	for _, path := range []string{link, filepath.Join(link, "absent.json")} {
		if _, err := sbxReadMCPRegistration(t.Context(), path); !errors.Is(err, ErrSBXBoundary) || errors.Is(err, os.ErrNotExist) {
			t.Fatal("dangling link was classified as permission to add a registration")
		}
	}
}

func TestSBXMCPStoredRegistrationNameAndArgumentAreFixed(t *testing.T) {
	for _, name := range []string{"", "unrelated", "traverse-empty-../secret", "traverse-empty-" + strings.Repeat("a", 31), "traverse-empty-" + strings.Repeat("G", 32)} {
		if sbxMCPHelperName(name) {
			t.Fatal("non-product registration path was accepted")
		}
	}
	executable := filepath.Join(t.TempDir(), "product-helper.exe")
	data := sbxTestMCPMetadataBytes(t, sbxTestMCPMetadata(executable))
	if err := sbxValidateMCPRegistration(data, sbxTestMCPHelperName, executable, sbxmcp.Arg+"=changed"); !errors.Is(err, ErrSBXBoundary) {
		t.Fatal("arbitrary helper mode was accepted")
	}
}
