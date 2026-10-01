package projectconfig

import (
	"errors"
	"fmt"
	"slices"
)

const InstructionDeliveryProtocolVersion = "project_instruction_delivery.v1"

type InstructionRequirement string

const (
	InstructionMandatory InstructionRequirement = "mandatory"
	InstructionOptional  InstructionRequirement = "optional"
	InstructionExcluded  InstructionRequirement = "excluded"
)

// InstructionSourceDelivery is an operator-confirmed classification of one
// exact source. Discovery never derives it from repository text. Exclusion is
// explicit and applies only to this pinned source, not to execution authority.
type InstructionSourceDelivery struct {
	Path            string                 `json:"path"`
	ContentSHA256   string                 `json:"content_sha256"`
	Requirement     InstructionRequirement `json:"requirement"`
	ExclusionReason string                 `json:"exclusion_reason,omitempty"`
}

type InstructionDelivery struct {
	ProtocolVersion string                      `json:"protocol_version"`
	Sources         []InstructionSourceDelivery `json:"sources"`
}

// ClassifyInstructionSnapshot requires every discovered source to be classified
// against its reviewed content hash. The snapshot fingerprint binds the decision
// as well as the original path, scope, precedence and content. Legacy snapshots
// retain their exact fingerprint; old readers reject classified fingerprints.
func ClassifyInstructionSnapshot(snapshot InstructionSnapshot,
	classifications []InstructionSourceDelivery,
) (InstructionSnapshot, error) {
	if err := snapshot.Validate(); err != nil {
		return InstructionSnapshot{}, err
	}
	if len(classifications) != len(snapshot.Sources) {
		return InstructionSnapshot{}, errors.New("explicit delivery classification is required for every project instruction source")
	}
	byPath := make(map[string]InstructionSourceDelivery, len(classifications))
	for _, item := range classifications {
		if _, duplicate := byPath[item.Path]; duplicate {
			return InstructionSnapshot{}, errors.New("project instruction delivery source is duplicated")
		}
		byPath[item.Path] = item
	}
	delivery := &InstructionDelivery{ProtocolVersion: InstructionDeliveryProtocolVersion,
		Sources: make([]InstructionSourceDelivery, len(snapshot.Sources))}
	for index, source := range snapshot.Sources {
		item, found := byPath[source.Path]
		if !found || item.ContentSHA256 != source.ContentSHA256 {
			return InstructionSnapshot{}, errors.New("project instruction delivery classification does not match the reviewed source")
		}
		delivery.Sources[index] = item
	}
	snapshot.Delivery = delivery
	snapshot.Fingerprint = snapshot.stableFingerprint()
	if err := snapshot.Validate(); err != nil {
		return InstructionSnapshot{}, err
	}
	return snapshot, nil
}

// MatchLiveInstructionDelivery retains classification only when the complete
// discovered source set, target and precedence still match the pinned content.
// Changed disk content remains an unclassified preview until explicit refresh.
func MatchLiveInstructionDelivery(pinned, live InstructionSnapshot) InstructionSnapshot {
	if pinned.Delivery == nil {
		return live
	}
	content := pinned
	content.Delivery = nil
	liveContent := live
	liveContent.Delivery = nil
	if content.stableFingerprint() != liveContent.stableFingerprint() {
		return live
	}
	live.Delivery = &InstructionDelivery{ProtocolVersion: pinned.Delivery.ProtocolVersion,
		Sources: slices.Clone(pinned.Delivery.Sources)}
	live.Fingerprint = live.stableFingerprint()
	return live
}

func (s InstructionSnapshot) validateDelivery() error {
	if s.Delivery == nil {
		return nil
	}
	if s.Delivery.ProtocolVersion != InstructionDeliveryProtocolVersion ||
		len(s.Delivery.Sources) != len(s.Sources) {
		return errors.New("project instruction delivery version or source count is invalid")
	}
	for index, item := range s.Delivery.Sources {
		source := s.Sources[index]
		if item.Path != source.Path || item.ContentSHA256 != source.ContentSHA256 {
			return errors.New("project instruction delivery source binding is invalid")
		}
		switch item.Requirement {
		case InstructionMandatory, InstructionOptional:
			if item.ExclusionReason != "" {
				return errors.New("included project instruction cannot have an exclusion reason")
			}
		case InstructionExcluded:
			switch item.ExclusionReason {
			case "superseded", "not_applicable", "withdrawn":
			default:
				return errors.New("excluded project instruction requires an explicit exclusion reason")
			}
		default:
			return errors.New("project instruction delivery requirement is invalid")
		}
	}
	return nil
}

// DeliverySourceID is bounded audit identity, not permission. The fingerprint
// locates the immutable snapshot containing source path/scope/priority/version.
func (s InstructionSnapshot) DeliverySourceID(index int) string {
	if s.Delivery == nil || index < 0 || index >= len(s.Delivery.Sources) {
		return ""
	}
	item := s.Delivery.Sources[index]
	return fmt.Sprintf("project-rule.v1/%s/%s/%s/%d", item.Requirement,
		s.Fingerprint, item.ContentSHA256, index+1)
}
