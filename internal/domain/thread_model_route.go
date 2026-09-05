package domain

import (
	"errors"
	"strings"
	"time"
)

const (
	ThreadModelRouteProtocolVersion        = "thread_model_route.v1"
	ThreadModelRouteControlProtocolVersion = "thread_model_route_control.v1"
)

type ThreadModelRouteAction string

const (
	ThreadModelRouteSelect ThreadModelRouteAction = "select"
	ThreadModelRouteReset  ThreadModelRouteAction = "reset"
)

// ThreadModelRoutePreference is the durable next-Run model selection for a
// Thread. Selected=false is an explicit reset tombstone; it prevents an older
// selection from becoming effective again while preserving an auditable update
// timestamp.
type ThreadModelRoutePreference struct {
	ProtocolVersion string    `json:"protocol_version"`
	ThreadID        string    `json:"thread_id"`
	Selected        bool      `json:"selected"`
	Provider        string    `json:"provider,omitempty"`
	Model           string    `json:"model,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (p ThreadModelRoutePreference) Validate() error {
	if p.ProtocolVersion != ThreadModelRouteProtocolVersion {
		return errors.New("unsupported Thread model route protocol")
	}
	if !ValidAgentID(p.ThreadID) || p.UpdatedAt.IsZero() {
		return errors.New("Thread model route identity and timestamp are required")
	}
	if !p.Selected {
		if p.Provider != "" || p.Model != "" {
			return errors.New("reset Thread model route cannot retain a Provider or model")
		}
		return nil
	}
	if p.Provider == "" || p.Model == "" || p.Provider != strings.TrimSpace(p.Provider) ||
		p.Model != strings.TrimSpace(p.Model) || !ValidAgentID(p.Provider) ||
		!ValidAgentID(p.Model) {
		return errors.New("selected Thread model route requires normalized Provider and model")
	}
	return nil
}

type ThreadModelRouteMutation struct {
	Version  string
	ThreadID string
	Action   ThreadModelRouteAction
	Provider string
	Model    string
	// CustomProvider and ExpectedProviderDefinitionRevision form the durable
	// definition CAS for a selection. The public request deliberately does not
	// carry these fields: the control plane derives them from the Registry
	// snapshot that admitted the route, and the Store rechecks them in the same
	// transaction that persists the preference.
	CustomProvider                     bool
	ExpectedProviderDefinitionRevision uint64
	OperationKey                       string
	RequestFingerprint                 string
	RequestedBy                        string
	At                                 time.Time
}

type ThreadModelRouteMutationResult struct {
	Preference ThreadModelRoutePreference
	Replayed   bool
}

// InitialThreadModelRoutePin carries the Registry admission fact for an
// explicit provider/model selected while the first Run is created. It is not a
// public request shape: the control plane derives CustomProvider and the
// expected durable definition revision from one Registry snapshot, then the
// Store rechecks them in the same transaction that creates the Thread, Run,
// Session, and preference.
type InitialThreadModelRoutePin struct {
	Provider                           string
	Model                              string
	CustomProvider                     bool
	ExpectedProviderDefinitionRevision uint64
}

func (p InitialThreadModelRoutePin) Empty() bool {
	return p.Provider == "" && p.Model == "" && !p.CustomProvider &&
		p.ExpectedProviderDefinitionRevision == 0
}

func (p InitialThreadModelRoutePin) Validate() error {
	if p.Empty() {
		return nil
	}
	if p.Provider == "" || p.Model == "" ||
		p.Provider != strings.TrimSpace(p.Provider) ||
		p.Model != strings.TrimSpace(p.Model) ||
		!ValidAgentID(p.Provider) || !ValidAgentID(p.Model) {
		return errors.New("initial Thread model route pin requires a normalized Provider and model")
	}
	if p.CustomProvider != (p.ExpectedProviderDefinitionRevision != 0) {
		return errors.New("initial custom Provider route pin requires an exact definition revision")
	}
	return nil
}
