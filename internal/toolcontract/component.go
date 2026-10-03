// Package toolcontract contains host-side component and tool execution contracts.
// It does not load plugins, expand variables, grant OS privileges, own a ledger,
// or execute operations. Plugin declarations are data, never runtime authority.
package toolcontract

import (
	"errors"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

type ComponentRef struct {
	PackageID   string
	ComponentID string
}

func (r ComponentRef) Validate() error {
	if !normalized(r.PackageID, 256) || !normalized(r.ComponentID, 256) {
		return errors.New("component identity is invalid")
	}
	return nil
}

type SourceRef struct {
	URI      string
	Revision string
	SHA256   string
}

func (r SourceRef) Validate() error {
	if !normalized(r.URI, 4096) || (r.Revision != "" && !normalized(r.Revision, 256)) ||
		(r.SHA256 != "" && !digest(r.SHA256)) {
		return errors.New("component source reference is invalid")
	}
	return nil
}

// ContentRef is package-relative. An empty digest means not loaded/verified;
// neither a reference nor its digest authorizes access to a filesystem root.
type ContentRef struct {
	Component ComponentRef
	Path      string
	SHA256    string
}

func (r ContentRef) Validate() error {
	if r.Component.Validate() != nil || !normalized(r.Path, 4096) ||
		strings.ContainsAny(r.Path, "\\:") || path.IsAbs(r.Path) ||
		path.Clean(r.Path) != r.Path || r.Path == "." || r.Path == ".." ||
		strings.HasPrefix(r.Path, "../") || (r.SHA256 != "" && !digest(r.SHA256)) {
		return errors.New("component content reference is invalid")
	}
	return nil
}

type Diagnostic struct {
	Component ComponentRef
	Code      string
	Severity  string
	Message   string // producer must exclude secrets and raw launch configuration
}

func (d Diagnostic) Validate() error {
	if d.Component.Validate() != nil || !normalized(d.Code, 128) ||
		!normalized(d.Message, 4096) ||
		(d.Severity != "info" && d.Severity != "warning" && d.Severity != "error") {
		return errors.New("component diagnostic is invalid")
	}
	return nil
}

func normalized(value string, limit int) bool {
	return value != "" && value == strings.TrimSpace(value) && text(value, limit)
}

func text(value string, limit int) bool {
	return len(value) <= limit && utf8.ValidString(value) &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func digest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
