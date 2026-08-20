package browserruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"cyberagent-workbench/internal/domain"
)

const (
	ProfileMarkerProtocolVersion       = "browser_profile_marker.v1"
	ProfileRuntimeLeaseProtocolVersion = "browser_profile_runtime_lease.v1"
	MaxProfileMarkerBytes              = 16 * 1024
	profileCleanupRetryTimeout         = 5 * time.Second
	profileCleanupRetryInterval        = 25 * time.Millisecond
)

var profileEnvironmentDirectoryNames = [...]string{"Temp", "LocalAppData", "RoamingAppData"}

type ProfileMarkerState string

const (
	ProfileMarkerActive   ProfileMarkerState = "active"
	ProfileMarkerReleased ProfileMarkerState = "released"
)

// ProfileOwnerMarker is the only ownership evidence trusted by the concrete
// filesystem adapter. It contains no host identity, secret, or cleanup grant.
type ProfileOwnerMarker struct {
	ProtocolVersion               string             `json:"protocol_version"`
	OwnershipPlanFingerprint      string             `json:"ownership_plan_fingerprint"`
	PreviousOwnershipFingerprint  string             `json:"previous_ownership_fingerprint,omitempty"`
	SessionPlanFingerprint        string             `json:"session_plan_fingerprint"`
	ExecutableIdentityFingerprint string             `json:"executable_identity_fingerprint"`
	ProfileToken                  string             `json:"profile_token"`
	OwnerToken                    string             `json:"owner_token"`
	MarkerPayloadSHA256           string             `json:"marker_payload_sha256"`
	Generation                    uint64             `json:"generation"`
	State                         ProfileMarkerState `json:"state"`
	CreatedAt                     time.Time          `json:"created_at"`
	ReleasedAt                    time.Time          `json:"released_at,omitempty"`
	PersonalProfile               bool               `json:"personal_profile"`
	ModelOwned                    bool               `json:"model_owned"`
	Fingerprint                   string             `json:"fingerprint"`
}

type ProfileRuntimeLease struct {
	ProtocolVersion           string             `json:"protocol_version"`
	OwnershipPlanFingerprint  string             `json:"ownership_plan_fingerprint"`
	AuthorizationFingerprint  string             `json:"authorization_fingerprint"`
	DirectoryPath             string             `json:"directory_path"`
	OwnerToken                string             `json:"owner_token"`
	MarkerFingerprint         string             `json:"marker_fingerprint"`
	Generation                uint64             `json:"generation"`
	State                     ProfileMarkerState `json:"state"`
	CreatedAt                 time.Time          `json:"created_at"`
	ReleasedAt                time.Time          `json:"released_at,omitempty"`
	PersonalProfile           bool               `json:"personal_profile"`
	ExactOwnedCleanupRequired bool               `json:"exact_owned_cleanup_required"`
	Fingerprint               string             `json:"fingerprint"`
}

func MaterializeDisposableProfile(authorization BrowserStartAuthorization,
	session SessionPlan, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, ownership ProfileOwnershipPlan,
	attempt BrowserLaunchAttempt, launchLease BrowserLaunchLease,
	review BrowserLaunchReview,
	networkEvidence BrowserNetworkContainmentEvidence,
	networkReview BrowserNetworkContainmentReview,
	networkPlan BrowserNetworkContainmentPlan,
	permission domain.RunBrowserCDPPermissionSnapshot,
	now time.Time,
) (ProfileRuntimeLease, error) {
	if err := ValidateBrowserStartAuthorization(authorization, session, identity,
		acceptance, ownership, attempt, launchLease, review, networkEvidence,
		networkReview, networkPlan, permission); err != nil {
		return ProfileRuntimeLease{}, err
	}
	now = now.UTC()
	if now.IsZero() || now.Before(authorization.IssuedAt) ||
		!now.Before(authorization.StartDeadline) {
		return ProfileRuntimeLease{}, errors.New("browser Profile materialization authorization expired")
	}
	if err := ensureProfileRuntimeRoot(ownership.RootPath); err != nil {
		return ProfileRuntimeLease{}, err
	}
	marker := newProfileOwnerMarker(ownership, now)
	if ownership.Generation == 1 {
		if err := createInitialProfileDirectory(ownership, marker); err != nil {
			return ProfileRuntimeLease{}, err
		}
	} else if err := recoverProfileDirectory(ownership, marker); err != nil {
		return ProfileRuntimeLease{}, err
	}
	if err := ensureProfileEnvironmentDirectories(ownership.DirectoryPath); err != nil {
		return ProfileRuntimeLease{}, err
	}
	lease := ProfileRuntimeLease{
		ProtocolVersion:           ProfileRuntimeLeaseProtocolVersion,
		OwnershipPlanFingerprint:  ownership.Fingerprint,
		AuthorizationFingerprint:  authorization.Fingerprint,
		DirectoryPath:             ownership.DirectoryPath,
		OwnerToken:                ownership.OwnerToken,
		MarkerFingerprint:         marker.Fingerprint,
		Generation:                ownership.Generation,
		State:                     ProfileMarkerActive,
		CreatedAt:                 marker.CreatedAt,
		ExactOwnedCleanupRequired: true,
	}
	lease.Fingerprint = browserRuntimeFingerprint(lease)
	if err := ValidateProfileRuntimeLease(lease, authorization, ownership); err != nil {
		return ProfileRuntimeLease{}, err
	}
	return lease, nil
}

func ValidateProfileRuntimeLease(lease ProfileRuntimeLease,
	authorization BrowserStartAuthorization, ownership ProfileOwnershipPlan,
) error {
	if err := validateProfileOwnershipStructure(ownership); err != nil {
		return err
	}
	if lease.ProtocolVersion != ProfileRuntimeLeaseProtocolVersion ||
		lease.OwnershipPlanFingerprint != ownership.Fingerprint ||
		lease.AuthorizationFingerprint != authorization.Fingerprint ||
		lease.DirectoryPath != ownership.DirectoryPath ||
		lease.OwnerToken != ownership.OwnerToken || !validSHA256(lease.MarkerFingerprint) ||
		lease.Generation != ownership.Generation || lease.State != ProfileMarkerActive ||
		lease.CreatedAt.IsZero() || !lease.ReleasedAt.IsZero() || lease.PersonalProfile ||
		!lease.ExactOwnedCleanupRequired ||
		lease.Fingerprint != browserRuntimeFingerprint(lease) {
		return errors.New("browser Profile runtime lease lost an exact ownership boundary")
	}
	return nil
}

// ObserveDisposableProfile reads only the bounded owner marker. The caller
// supplies process liveness from the process-tree owner; marker contents never
// decide that a process is alive.
func ObserveDisposableProfile(ownership ProfileOwnershipPlan,
	processActive bool,
) (ProfileDirectoryObservation, error) {
	if err := validateProfileOwnershipStructure(ownership); err != nil {
		return ProfileDirectoryObservation{}, err
	}
	info, err := os.Lstat(ownership.DirectoryPath)
	if errors.Is(err, os.ErrNotExist) {
		return BuildProfileDirectoryObservation(ownership, ProfileDirectoryAbsent, "", 0, "")
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		!profilePathHasNoIndirection(ownership.DirectoryPath) {
		return BuildProfileDirectoryObservation(ownership, ProfileDirectoryCorrupt, "", 0, "")
	}
	marker, err := readProfileOwnerMarker(ownership.DirectoryPath)
	if err != nil {
		return BuildProfileDirectoryObservation(ownership, ProfileDirectoryCorrupt, "", 0, "")
	}
	state := ProfileDirectoryForeign
	if markerMatchesOwnership(marker, ownership) {
		switch marker.State {
		case ProfileMarkerReleased:
			state = ProfileDirectoryOwnedReleased
		case ProfileMarkerActive:
			if processActive {
				state = ProfileDirectoryOwnedActive
			} else {
				state = ProfileDirectoryOwnedStale
			}
		}
	}
	return BuildProfileDirectoryObservation(ownership, state, marker.OwnerToken,
		marker.Generation, marker.MarkerPayloadSHA256)
}

func ReleaseDisposableProfile(authorization BrowserStartAuthorization,
	lease ProfileRuntimeLease, ownership ProfileOwnershipPlan,
	processTreeQuiescent bool, now time.Time,
) (ProfileRuntimeLease, error) {
	if err := ValidateProfileRuntimeLease(lease, authorization, ownership); err != nil {
		return ProfileRuntimeLease{}, err
	}
	now = now.UTC()
	if !authorization.ProfileReleaseAuthorized || !processTreeQuiescent || now.IsZero() ||
		now.Before(lease.CreatedAt) {
		return ProfileRuntimeLease{}, errors.New("browser Profile release requires a quiescent exact process tree")
	}
	marker, err := readProfileOwnerMarker(ownership.DirectoryPath)
	if err != nil || !markerMatchesOwnership(marker, ownership) ||
		marker.State != ProfileMarkerActive {
		return ProfileRuntimeLease{}, errors.New("browser Profile release marker no longer matches its active owner")
	}
	marker.State = ProfileMarkerReleased
	marker.ReleasedAt = now
	marker.Fingerprint = ""
	marker.Fingerprint = browserRuntimeFingerprint(marker)
	if err := writeProfileOwnerMarkerAtomic(ownership.DirectoryPath, marker); err != nil {
		return ProfileRuntimeLease{}, err
	}
	released := lease
	released.State = ProfileMarkerReleased
	released.ReleasedAt = now
	released.MarkerFingerprint = marker.Fingerprint
	released.Fingerprint = ""
	released.Fingerprint = browserRuntimeFingerprint(released)
	return released, nil
}

func CleanupReleasedProfile(authorization BrowserStartAuthorization,
	lease ProfileRuntimeLease, ownership ProfileOwnershipPlan,
	processTreeQuiescent bool,
) error {
	if lease.ProtocolVersion != ProfileRuntimeLeaseProtocolVersion ||
		lease.AuthorizationFingerprint != authorization.Fingerprint ||
		lease.OwnershipPlanFingerprint != ownership.Fingerprint ||
		lease.DirectoryPath != ownership.DirectoryPath || lease.OwnerToken != ownership.OwnerToken ||
		lease.Generation != ownership.Generation || lease.State != ProfileMarkerReleased ||
		lease.ReleasedAt.IsZero() || lease.PersonalProfile ||
		!lease.ExactOwnedCleanupRequired || lease.Fingerprint != browserRuntimeFingerprint(lease) ||
		!authorization.ExactOwnedCleanupAuthorized || !processTreeQuiescent {
		return errors.New("browser Profile cleanup requires an exact released owner and quiescent tree")
	}
	if err := ensureProfileRuntimeRoot(ownership.RootPath); err != nil {
		return err
	}
	marker, err := readProfileOwnerMarker(ownership.DirectoryPath)
	if err != nil || !markerMatchesOwnership(marker, ownership) ||
		marker.State != ProfileMarkerReleased || marker.Fingerprint != lease.MarkerFingerprint {
		return errors.New("browser Profile cleanup marker no longer matches its released owner")
	}
	if !pathWithinRoot(ownership.RootPath, ownership.DirectoryPath) ||
		filepath.Base(ownership.DirectoryPath) != ownership.DirectoryName ||
		!profilePathHasNoIndirection(ownership.DirectoryPath) {
		return errors.New("browser Profile cleanup path is not the exact owned directory")
	}
	quarantineName := ".cleanup-" + ownership.OwnerToken[:16] + "-" +
		strconv.FormatUint(ownership.Generation, 10)
	quarantinePath := filepath.Join(ownership.RootPath, quarantineName)
	if !pathWithinRoot(ownership.RootPath, quarantinePath) ||
		filepath.Base(quarantinePath) != quarantineName {
		return errors.New("browser Profile cleanup quarantine escaped its runtime root")
	}
	if _, err := os.Lstat(quarantinePath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("browser Profile cleanup quarantine already exists")
	}
	if err := os.Rename(ownership.DirectoryPath, quarantinePath); err != nil {
		return fmt.Errorf("rename exact browser Profile for cleanup: %w", err)
	}
	quarantinedMarker, verifyErr := readProfileOwnerMarker(quarantinePath)
	if verifyErr != nil || !markerMatchesOwnership(quarantinedMarker, ownership) ||
		quarantinedMarker.State != ProfileMarkerReleased {
		_ = os.Rename(quarantinePath, ownership.DirectoryPath)
		return errors.New("renamed browser Profile failed its final owner recheck")
	}
	if !profilePathHasNoIndirection(quarantinePath) {
		_ = os.Rename(quarantinePath, ownership.DirectoryPath)
		return errors.New("renamed browser Profile became an indirect path")
	}
	if err := removeProfileTreeBounded(quarantinePath, profileCleanupRetryTimeout,
		os.RemoveAll); err != nil {
		return fmt.Errorf("remove exact released browser Profile: %w", err)
	}
	return nil
}

func removeProfileTreeBounded(path string, timeout time.Duration,
	remove func(string) error,
) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) == path ||
		timeout <= 0 || remove == nil {
		return errors.New("browser Profile cleanup retry target is invalid")
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(profileCleanupRetryInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		if err := remove(path); err != nil {
			lastErr = err
		} else if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			lastErr = err
		} else {
			lastErr = errors.New("browser Profile cleanup left the exact directory present")
		}
		select {
		case <-deadline.C:
			return errors.Join(errors.New("browser Profile cleanup retry limit exhausted"), lastErr)
		case <-ticker.C:
		}
	}
}

func newProfileOwnerMarker(ownership ProfileOwnershipPlan, now time.Time) ProfileOwnerMarker {
	marker := ProfileOwnerMarker{
		ProtocolVersion:               ProfileMarkerProtocolVersion,
		OwnershipPlanFingerprint:      ownership.Fingerprint,
		PreviousOwnershipFingerprint:  ownership.PreviousOwnershipFingerprint,
		SessionPlanFingerprint:        ownership.SessionPlanFingerprint,
		ExecutableIdentityFingerprint: ownership.ExecutableIdentityFingerprint,
		ProfileToken:                  ownership.ProfileToken,
		OwnerToken:                    ownership.OwnerToken,
		MarkerPayloadSHA256:           ownership.MarkerPayloadSHA256,
		Generation:                    ownership.Generation,
		State:                         ProfileMarkerActive,
		CreatedAt:                     now.UTC(),
	}
	marker.Fingerprint = browserRuntimeFingerprint(marker)
	return marker
}

func validateProfileOwnerMarker(marker ProfileOwnerMarker) error {
	if marker.ProtocolVersion != ProfileMarkerProtocolVersion ||
		!validSHA256(marker.OwnershipPlanFingerprint) ||
		(marker.PreviousOwnershipFingerprint != "" &&
			!validSHA256(marker.PreviousOwnershipFingerprint)) ||
		!validSHA256(marker.SessionPlanFingerprint) ||
		!validSHA256(marker.ExecutableIdentityFingerprint) ||
		!validSHA256(marker.ProfileToken) || !validSHA256(marker.OwnerToken) ||
		!validSHA256(marker.MarkerPayloadSHA256) || marker.Generation == 0 ||
		marker.Generation > MaxProfileOwnershipGeneration || marker.CreatedAt.IsZero() ||
		marker.PersonalProfile || marker.ModelOwned {
		return errors.New("browser Profile owner marker is invalid")
	}
	switch marker.State {
	case ProfileMarkerActive:
		if !marker.ReleasedAt.IsZero() {
			return errors.New("active browser Profile marker contains a release time")
		}
	case ProfileMarkerReleased:
		if marker.ReleasedAt.IsZero() || marker.ReleasedAt.Before(marker.CreatedAt) {
			return errors.New("released browser Profile marker has an invalid release time")
		}
	default:
		return errors.New("browser Profile owner marker state is invalid")
	}
	if marker.Fingerprint != browserRuntimeFingerprint(marker) {
		return errors.New("browser Profile owner marker fingerprint mismatch")
	}
	return nil
}

func markerMatchesOwnership(marker ProfileOwnerMarker,
	ownership ProfileOwnershipPlan,
) bool {
	return validateProfileOwnerMarker(marker) == nil &&
		marker.OwnershipPlanFingerprint == ownership.Fingerprint &&
		marker.PreviousOwnershipFingerprint == ownership.PreviousOwnershipFingerprint &&
		marker.SessionPlanFingerprint == ownership.SessionPlanFingerprint &&
		marker.ExecutableIdentityFingerprint == ownership.ExecutableIdentityFingerprint &&
		marker.ProfileToken == ownership.ProfileToken && marker.OwnerToken == ownership.OwnerToken &&
		marker.MarkerPayloadSHA256 == ownership.MarkerPayloadSHA256 &&
		marker.Generation == ownership.Generation
}

func createInitialProfileDirectory(ownership ProfileOwnershipPlan,
	marker ProfileOwnerMarker,
) error {
	if ownership.Generation != 1 || ownership.PreviousOwnershipFingerprint != "" {
		return errors.New("initial browser Profile creation requires generation one")
	}
	if _, err := os.Lstat(ownership.DirectoryPath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("browser Profile directory already exists")
	}
	if err := os.Mkdir(ownership.DirectoryPath, 0o700); err != nil {
		return fmt.Errorf("create disposable browser Profile: %w", err)
	}
	if !profilePathHasNoIndirection(ownership.DirectoryPath) {
		_ = os.Remove(ownership.DirectoryPath)
		return errors.New("created browser Profile directory is indirect")
	}
	if err := writeProfileOwnerMarkerExclusive(ownership.DirectoryPath, marker); err != nil {
		_ = os.Remove(ownership.DirectoryPath)
		return err
	}
	return nil
}

func recoverProfileDirectory(ownership ProfileOwnershipPlan,
	marker ProfileOwnerMarker,
) error {
	if ownership.Generation <= 1 || !validSHA256(ownership.PreviousOwnershipFingerprint) {
		return errors.New("browser Profile recovery requires generation ancestry")
	}
	if !profilePathHasNoIndirection(ownership.DirectoryPath) {
		return errors.New("browser Profile recovery path is unavailable or indirect")
	}
	previous, err := readProfileOwnerMarker(ownership.DirectoryPath)
	if err != nil || previous.OwnershipPlanFingerprint != ownership.PreviousOwnershipFingerprint ||
		previous.Generation+1 != ownership.Generation || previous.State != ProfileMarkerActive ||
		previous.PersonalProfile || previous.ModelOwned {
		return errors.New("browser Profile recovery marker does not match the previous stale generation")
	}
	return writeProfileOwnerMarkerAtomic(ownership.DirectoryPath, marker)
}

func ensureProfileRuntimeRoot(rootPath string) error {
	if !validProfileRuntimeRoot(rootPath) {
		return errors.New("browser Profile runtime root is invalid")
	}
	parent := filepath.Dir(rootPath)
	if !profilePathHasNoIndirection(parent) {
		return errors.New("browser Profile runtime parent is unavailable or indirect")
	}
	if err := os.Mkdir(rootPath, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create browser Profile runtime root: %w", err)
	}
	info, err := os.Lstat(rootPath)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		!profilePathHasNoIndirection(rootPath) {
		return errors.New("browser Profile runtime root is not a direct directory")
	}
	return nil
}

func ensureProfileEnvironmentDirectories(profilePath string) error {
	for _, name := range profileEnvironmentDirectoryNames {
		path := filepath.Join(profilePath, name)
		if !pathWithinRoot(profilePath, path) {
			return ErrBrowserRuntimeBoundary
		}
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if !profilePathHasNoIndirection(path) {
			return errors.New("browser environment directory is indirect")
		}
	}
	return nil
}

func writeProfileOwnerMarkerExclusive(directory string, marker ProfileOwnerMarker) error {
	if err := validateProfileOwnerMarker(marker); err != nil {
		return err
	}
	raw, err := json.Marshal(marker)
	if err != nil || len(raw) == 0 || len(raw) > MaxProfileMarkerBytes {
		return errors.New("browser Profile owner marker is oversized")
	}
	path := filepath.Join(directory, ProfileOwnerMarkerName)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create browser Profile owner marker: %w", err)
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	return closeErr
}

func writeProfileOwnerMarkerAtomic(directory string, marker ProfileOwnerMarker) error {
	if err := validateProfileOwnerMarker(marker); err != nil {
		return err
	}
	raw, err := json.Marshal(marker)
	if err != nil || len(raw) == 0 || len(raw) > MaxProfileMarkerBytes {
		return errors.New("browser Profile owner marker is oversized")
	}
	temporary := filepath.Join(directory, ".prayu-owner-"+marker.Fingerprint[:16]+".tmp")
	final := filepath.Join(directory, ProfileOwnerMarkerName)
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary browser Profile marker: %w", err)
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(temporary)
		return errors.Join(err, closeErr)
	}
	if err := platformReplaceProfileMarker(temporary, final); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace browser Profile owner marker: %w", err)
	}
	return nil
}

func readProfileOwnerMarker(directory string) (ProfileOwnerMarker, error) {
	path := filepath.Join(directory, ProfileOwnerMarkerName)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > MaxProfileMarkerBytes ||
		!profilePathHasNoIndirection(path) {
		return ProfileOwnerMarker{}, errors.New("browser Profile owner marker is unavailable or indirect")
	}
	file, err := os.Open(path)
	if err != nil {
		return ProfileOwnerMarker{}, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, MaxProfileMarkerBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > MaxProfileMarkerBytes {
		return ProfileOwnerMarker{}, errors.New("browser Profile owner marker is unreadable or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var marker ProfileOwnerMarker
	if err := decoder.Decode(&marker); err != nil {
		return ProfileOwnerMarker{}, errors.New("browser Profile owner marker is malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ProfileOwnerMarker{}, errors.New("browser Profile owner marker has trailing data")
	}
	if err := validateProfileOwnerMarker(marker); err != nil {
		return ProfileOwnerMarker{}, err
	}
	canonical, err := json.Marshal(marker)
	if err != nil || !bytes.Equal(raw, canonical) {
		return ProfileOwnerMarker{}, errors.New("browser Profile owner marker is not canonical")
	}
	return marker, nil
}

func profilePathHasNoIndirection(path string) bool {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && samePath(path, resolved) && platformProfilePathDirect(path)
}
