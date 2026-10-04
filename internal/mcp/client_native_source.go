package mcp

import (
	"context"
	"errors"

	"cyberagent-workbench/internal/toolcontract"
)

// NativeClientProtocolVersion describes a host registration by acquired source
// reference. It is not an Agent Plugins author format or an MCP wire profile.
const NativeClientProtocolVersion = "mcp-client.v2"

// NativeSourceRef contains only existing host installation identities. Original
// launch fields remain in the acquired package object, never in a lossy legacy
// descriptor projection. Changing enablement requires an explicit new staging.
type NativeSourceRef struct {
	InstallationID         string                    `json:"installation_id"`
	Component              toolcontract.ComponentRef `json:"component"`
	Revision               string                    `json:"revision"`
	InstallationGeneration int64                     `json:"installation_generation"`
	Surface                string                    `json:"surface"`
}

func (r NativeSourceRef) Validate() error {
	if !validClientIdentity(r.InstallationID) || r.Component.Validate() != nil ||
		!validClientDigest(r.Revision) || r.InstallationGeneration < 1 ||
		(r.Surface != "code" && r.Surface != "cyber") {
		return errors.New("native MCP source reference is invalid")
	}
	return nil
}

// NativeSourceResolver is host wiring around the existing installation/object
// store. Plugins provide neither this resolver nor any authorization callbacks.
// Empty runID is the explicit operator discovery path; otherwise Check must also
// verify that the Run's surface matches the installation's selected surface.
type NativeSourceResolver interface {
	Check(context.Context, NativeSourceRef, string) error
	Resolve(context.Context, NativeSourceRef, string) (toolcontract.ResolvedLaunch, []string, func(context.Context) error, func(), error)
}
