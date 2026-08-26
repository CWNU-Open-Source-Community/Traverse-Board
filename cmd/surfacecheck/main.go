// Command surfacecheck validates ADR 0135's machine-readable Surface tier
// registry and checks that its generated inventory and PR declaration gate are
// synchronized.
package main

import (
	"flag"
	"fmt"
	"os"

	"cyberagent-workbench/internal/surfacegovernance"
)

func main() {
	registryPath := flag.String("registry", "docs/convergence/surface-registry.json",
		"path to the machine-readable Surface registry")
	baseRegistryPath := flag.String("base-registry", os.Getenv("SURFACE_REGISTRY_BASE"),
		"optional reviewed base registry used to enforce append-only transitions and tombstones")
	documentPath := flag.String("document", "docs/convergence/surface-inventory.md",
		"path to the generated Surface inventory")
	templatePath := flag.String("template", ".github/PULL_REQUEST_TEMPLATE.md",
		"path to the pull request template")
	writeDocument := flag.Bool("write", false, "rewrite the generated inventory")
	flag.Parse()

	registry, err := surfacegovernance.LoadFile(*registryPath)
	if err != nil {
		fail(err)
	}
	generated, err := surfacegovernance.Render(registry)
	if err != nil {
		fail(err)
	}
	if *baseRegistryPath != "" {
		baseRegistry, err := surfacegovernance.LoadFile(*baseRegistryPath)
		if err != nil {
			fail(fmt.Errorf("load base Surface registry: %w", err))
		}
		if err := surfacegovernance.ValidateEvolution(baseRegistry, registry); err != nil {
			fail(err)
		}
	}

	template, err := os.ReadFile(*templatePath)
	if err != nil {
		fail(err)
	}
	if err := surfacegovernance.ValidatePullRequestTemplate(template); err != nil {
		fail(err)
	}

	if *writeDocument {
		if err := os.WriteFile(*documentPath, generated, 0o644); err != nil {
			fail(err)
		}
		fmt.Printf("surface_inventory_written: %s\n", *documentPath)
		return
	}
	if err := surfacegovernance.CheckDocument(*documentPath, generated); err != nil {
		fail(err)
	}
	fmt.Printf("surface_registry_valid: %s\nsurface_inventory_current: %s\n",
		*registryPath, *documentPath)
	if *baseRegistryPath != "" {
		fmt.Printf("surface_registry_evolution_valid: %s\n", *baseRegistryPath)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "surfacecheck:", err)
	os.Exit(1)
}
