package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"cyberagent-workbench/internal/protocolregistry"
)

var gitObjectIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{40,64}$`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "protocol registry:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("protocolregistry", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	root := flags.String("root", ".", "repository root")
	check := flags.Bool("check", false, "verify registry, source inventory, history, and generated document")
	write := flags.Bool("write", false, "write the generated Markdown document")
	baseline := flags.String("baseline", "", "optional full Git object id for append-only history checks")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 || *check == *write {
		return errors.New("select exactly one of -check or -write and pass no positional arguments")
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	registry, err := protocolregistry.LoadFile(filepath.Join(absRoot, filepath.FromSlash(protocolregistry.RegistryPath)))
	if err != nil {
		return err
	}
	if err := protocolregistry.ValidateRepositoryPaths(absRoot, registry); err != nil {
		return err
	}
	inventory, err := protocolregistry.Discover(absRoot, registry.Scan)
	if err != nil {
		return err
	}
	if err := protocolregistry.CompareInventory(registry, inventory); err != nil {
		return err
	}
	if err := protocolregistry.CheckRuntimeAuthorityBoundary(absRoot); err != nil {
		return err
	}
	if strings.Trim(*baseline, "0") != "" {
		previous, present, err := loadBaseline(absRoot, *baseline)
		if err != nil {
			return err
		}
		if present {
			if err := protocolregistry.ValidateEvolution(previous, registry); err != nil {
				return err
			}
		}
	}
	document := protocolregistry.RenderMarkdown(registry)
	documentPath := filepath.Join(absRoot, filepath.FromSlash(protocolregistry.GeneratedDocument))
	if *write {
		if err := os.WriteFile(documentPath, document, 0o644); err != nil {
			return fmt.Errorf("write generated protocol registry: %w", err)
		}
		fmt.Printf("wrote %s\n", protocolregistry.GeneratedDocument)
		return nil
	}
	committed, err := os.ReadFile(documentPath)
	if err != nil {
		return fmt.Errorf("read generated protocol registry: %w", err)
	}
	if !bytes.Equal(committed, document) {
		return fmt.Errorf("%s is stale; run go run ./cmd/protocolregistry -write", protocolregistry.GeneratedDocument)
	}
	fmt.Printf("protocol registry is synchronized (%d families, %d identifiers, %d explicit test/golden entries)\n",
		len(registry.Families), len(inventory), len(registry.TestAndGoldenAllowlist))
	return nil
}

func loadBaseline(root, ref string) (protocolregistry.Registry, bool, error) {
	if !gitObjectIDPattern.MatchString(ref) {
		return protocolregistry.Registry{}, false, errors.New("baseline must be a full hexadecimal Git object id")
	}
	commit := exec.Command("git", "-C", root, "cat-file", "-e", ref+"^{commit}")
	if output, err := commit.CombinedOutput(); err != nil {
		return protocolregistry.Registry{}, false, fmt.Errorf("validate baseline commit: %w: %s", err, strings.TrimSpace(string(output)))
	}
	listing := exec.Command("git", "-C", root, "ls-tree", "-z", "--name-only", ref, "--", protocolregistry.RegistryPath)
	listed, err := listing.Output()
	if err != nil {
		return protocolregistry.Registry{}, false, fmt.Errorf("inspect baseline registry: %w", err)
	}
	if len(listed) == 0 {
		return protocolregistry.Registry{}, false, nil
	}
	if string(listed) != protocolregistry.RegistryPath+"\x00" {
		return protocolregistry.Registry{}, false, errors.New("baseline registry lookup returned an unexpected path")
	}
	show := exec.Command("git", "-C", root, "show", ref+":"+protocolregistry.RegistryPath)
	raw, err := show.Output()
	if err != nil {
		return protocolregistry.Registry{}, false, fmt.Errorf("read baseline registry: %w", err)
	}
	registry, err := protocolregistry.Decode(raw)
	if err != nil {
		return protocolregistry.Registry{}, false, fmt.Errorf("decode baseline registry: %w", err)
	}
	return registry, true, nil
}
