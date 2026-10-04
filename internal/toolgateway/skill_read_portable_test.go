package toolgateway

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInstalledSkillReadRequiresExactReferenceAndContainedResource(t *testing.T) {
	pin := SkillReadRequest{InstallationID: "plugin-import-test", PackageID: "portable-test", ComponentID: "skills/test", Revision: strings.Repeat("a", 64), InstallationGeneration: 3}
	for _, resource := range []string{"", "references/readme.md", "scripts/__init__.py", "assets/data.bin"} {
		input := pin
		input.Resource = resource
		raw, _ := json.Marshal(input)
		got, canonical, err := NormalizeSkillReadPayload(raw)
		if err != nil || got != input || !json.Valid(canonical) || got.CatalogRequest() != pin {
			t.Fatalf("exact read lost: %+v %v", got, err)
		}
	}
	for _, resource := range []string{"../outside", "/absolute", "a/../outside", "C:/outside", "\\host\\file", "a\\b", ".", "a//b", "stream:secret", "nul\x00"} {
		input := pin
		input.Resource = resource
		raw, _ := json.Marshal(input)
		if _, _, err := NormalizeSkillReadPayload(raw); err == nil {
			t.Fatal("accepted escaping resource", resource)
		}
	}
	for _, mutate := range []func(*SkillReadRequest){
		func(p *SkillReadRequest) { p.Name = "bundled" }, func(p *SkillReadRequest) { p.InstallationGeneration = 0 },
		func(p *SkillReadRequest) { p.InstallationID = "bad\nidentity" }, func(p *SkillReadRequest) { p.PackageID = "" },
		func(p *SkillReadRequest) { p.Revision = "latest" }, func(p *SkillReadRequest) { p.ComponentID = "" },
	} {
		input := pin
		mutate(&input)
		raw, _ := json.Marshal(input)
		if _, _, err := NormalizeSkillReadPayload(raw); err == nil {
			t.Fatalf("accepted inexact native reference: %+v", input)
		}
	}
	legacy := SkillReadRequest{Name: "frontend-design", Version: "1.0.0", ContentSHA256: strings.Repeat("b", 64)}
	raw, _ := json.Marshal(legacy)
	got, _, err := NormalizeSkillReadPayload(raw)
	if err != nil || got != legacy {
		t.Fatal("old reference no longer readable", err)
	}
}
