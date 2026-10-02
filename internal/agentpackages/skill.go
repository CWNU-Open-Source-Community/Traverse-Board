package agentpackages

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

func inspectSkill(ctx context.Context, source fs.FS, directory string) (skillDescription, error) {
	return inspectNamedSkill(ctx, source, directory, path.Base(directory))
}

// A standalone skill opened as os.OpenRoot(selectedDirectory) uses directory
// "." and the host-observed directory basename. It never needs its parent root.
func inspectNamedSkill(ctx context.Context, source fs.FS, directory, directoryName string) (skillDescription, error) {
	if !fs.ValidPath(directory) {
		return skillDescription{}, errors.New("skill_directory_invalid")
	}
	entries, err := directoryEntries(ctx, source, directory)
	if err != nil {
		return skillDescription{}, err
	}
	found := false
	for _, entry := range entries {
		if entry.Name() == "SKILL.md" {
			found = true
			break
		}
	}
	if !found {
		return skillDescription{}, errMissingSkill
	}
	file := path.Join(directory, "SKILL.md")
	raw, err := readBounded(ctx, source, file, maxDocumentBytes)
	if err != nil {
		return skillDescription{}, err
	}
	if !utf8.Valid(raw) {
		return skillDescription{}, errors.New("skill_utf8_invalid")
	}
	frontmatter, header, err := splitFrontmatter(raw)
	if err != nil {
		return skillDescription{}, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(header, &document); err != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return skillDescription{}, errors.New("skill_frontmatter_invalid")
	}
	for i := 0; i < len(document.Content[0].Content); i += 2 {
		key := document.Content[0].Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return skillDescription{}, errors.New("skill_frontmatter_key_invalid")
		}
	}
	var values map[string]any
	if err := document.Decode(&values); err != nil {
		return skillDescription{}, errors.New("skill_frontmatter_invalid")
	}
	for key := range values {
		switch key {
		case "name", "description", "license", "compatibility", "allowed-tools", "metadata":
		default:
			return skillDescription{}, errors.New("skill_frontmatter_field_unsupported")
		}
	}
	name, ok := values["name"].(string)
	if !ok || !validSkillName(name) || name != directoryName {
		return skillDescription{}, errors.New("skill_name_invalid")
	}
	description, ok := values["description"].(string)
	if !ok || strings.TrimSpace(description) == "" || utf8.RuneCountInString(description) > 1024 {
		return skillDescription{}, errors.New("skill_description_invalid")
	}
	result := skillDescription{name: name, description: description, frontmatter: bytes.Clone(frontmatter), instructions: reference(file, raw), root: directory}
	for key, destination := range map[string]*string{"license": &result.license, "compatibility": &result.compatibility, "allowed-tools": &result.allowedTools} {
		if value, found := values[key]; found {
			var valid bool
			*destination, valid = value.(string)
			if !valid {
				return skillDescription{}, errors.New("skill_optional_field_invalid")
			}
		}
	}
	if _, present := values["compatibility"]; present && (strings.TrimSpace(result.compatibility) == "" || utf8.RuneCountInString(result.compatibility) > 500) {
		return skillDescription{}, errors.New("skill_compatibility_invalid")
	}
	if value, present := values["metadata"]; present {
		metadata, ok := value.(map[string]any)
		if !ok {
			return skillDescription{}, errors.New("skill_metadata_invalid")
		}
		result.metadata = make(map[string]string, len(metadata))
		for key, value := range metadata {
			text, ok := value.(string)
			if !ok {
				return skillDescription{}, errors.New("skill_metadata_invalid")
			}
			result.metadata[key] = text
		}
	}
	return result, nil
}

// Keep the original YAML delimiters, line endings and bytes. Client-specific
// fields are not interpreted as portable configuration or host policy.
func splitFrontmatter(raw []byte) (frontmatter, header []byte, err error) {
	start := 0
	if bytes.HasPrefix(raw, []byte{0xef, 0xbb, 0xbf}) {
		start = 3
	}
	line, next := sourceLine(raw, start)
	if string(line) != "---" {
		return nil, nil, errors.New("skill_frontmatter_missing")
	}
	headerStart := next
	for position := next; position < len(raw); {
		line, next = sourceLine(raw, position)
		if string(line) == "---" {
			return raw[:next], raw[headerStart:position], nil
		}
		position = next
	}
	return nil, nil, errors.New("skill_frontmatter_unterminated")
}

func sourceLine(raw []byte, start int) ([]byte, int) {
	end := bytes.IndexByte(raw[start:], '\n')
	next := len(raw)
	if end < 0 {
		end = len(raw)
	} else {
		end += start
		next = end + 1
	}
	return bytes.TrimSuffix(raw[start:end], []byte{'\r'}), next
}

func validSkillName(name string) bool {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 64 || strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") || strings.Contains(name, "--") {
		return false
	}
	for _, r := range name {
		if r != '-' && ((!unicode.IsLetter(r) && !unicode.IsNumber(r)) || unicode.ToLower(r) != r) {
			return false
		}
	}
	return true
}
