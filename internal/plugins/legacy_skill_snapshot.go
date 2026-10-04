package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"reflect"
	"sort"

	"cyberagent-workbench/internal/agentpackages"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/toolcontract"
)

// CaptureLegacySkill uses the existing strict Skill codec, including signature
// verification, but stores new installations through the Plugin lifecycle.
// The original archive and instruction bytes are retained without conversion.
func CaptureLegacySkill(ctx context.Context, raw []byte, packageID string,
	source toolcontract.SourceRef,
) (PortablePackage, error) {
	if err := ctx.Err(); err != nil {
		return PortablePackage{}, err
	}
	raw = bytes.Clone(raw)
	parsed, err := skills.ParsePackageAny(raw)
	if err != nil {
		return PortablePackage{}, err
	}
	preview := parsed.Preview()
	snapshot := PortableSnapshot{ProtocolVersion: PortableSnapshotProtocol,
		PackageID: packageID, Revision: digest(raw), RootName: preview.Manifest.Name,
		Format: agentpackages.FormatLegacySkill, Name: preview.Manifest.Name,
		AuthorVersion: preview.Manifest.Version, Source: source, Legacy: &preview}
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return PortablePackage{}, err
	}
	for _, file := range archive.File {
		value, err := readZipFile(file, int64(skills.MaxPackageUncompressedBytes))
		if err != nil {
			return PortablePackage{}, err
		}
		snapshot.Inventory = append(snapshot.Inventory, SnapshotEntry{Path: file.Name,
			Kind: "file", SHA256: digest(value), Bytes: len(value), Mode: uint32(file.Mode().Perm())})
		if file.Name == skills.PackageManifestPath {
			snapshot.Manifest = toolcontract.ContentRef{Component: toolcontract.ComponentRef{
				PackageID: packageID, ComponentID: "package"}, Path: file.Name, SHA256: digest(value)}
		}
		if file.Name == skills.PackageContentPath {
			snapshot.Skills = []SnapshotSkill{{Instructions: toolcontract.ContentRef{
				Component: toolcontract.ComponentRef{PackageID: packageID, ComponentID: "skill:" + preview.Manifest.Name},
				Path:      file.Name, SHA256: digest(value)}, Name: preview.Manifest.Name, Description: preview.Manifest.Description}}
		}
	}
	sort.Slice(snapshot.Inventory, func(i, j int) bool { return snapshot.Inventory[i].Path < snapshot.Inventory[j].Path })
	if err := snapshot.Validate(); err != nil {
		return PortablePackage{}, err
	}
	return PortablePackage{Snapshot: snapshot, raw: raw}, ctx.Err()
}

func openLegacySkillSnapshot(ctx context.Context, snapshot PortableSnapshot, raw []byte) (*PortableReader, error) {
	pkg, err := CaptureLegacySkill(ctx, raw, snapshot.PackageID, snapshot.Source)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(pkg.Snapshot, snapshot) {
		return nil, errors.New("legacy Skill description does not match its acquired archive")
	}
	archive, err := zip.NewReader(bytes.NewReader(pkg.raw), int64(len(pkg.raw)))
	if err != nil {
		return nil, err
	}
	for _, file := range archive.File {
		if file.Name == skills.PackageContentPath {
			body, err := readZipFile(file, int64(skills.MaxContentBytes))
			if err != nil {
				return nil, err
			}
			return &PortableReader{snapshot: pkg.Snapshot, legacy: body}, nil
		}
	}
	return nil, errors.New("legacy Skill instructions are absent")
}
