package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/outputsafe"
	"cyberagent-workbench/internal/redact"
)

const (
	MaxCommandRuntimeJobsPerRun    = 32
	MaxCommandRuntimeOutputRead    = 64 * 1024
	MaxCommandRuntimeWait          = 5 * time.Second
	MaxCommandRuntimeCancelGrace   = 5 * time.Second
	MaxCommandRuntimeStdinWrites   = 64
	MaxCommandRuntimeStoredIntent  = 256 * 1024
	MaxCommandRuntimeStoredOutput  = MaxCommandRuntimeArtifactBytes
	commandRuntimeReadPollInterval = 20 * time.Millisecond
)

var (
	ErrCommandRuntimeJobNotFound = errors.New("command runtime job was not found")
	ErrCommandRuntimeJobClosed   = errors.New("command runtime job is not running")
	ErrCommandRuntimeUncertain   = errors.New("command runtime operation is uncertain and cannot be replayed")
)

type CommandRuntimeJobState string

const (
	CommandRuntimeJobPrepared    CommandRuntimeJobState = "prepared"
	CommandRuntimeJobRunning     CommandRuntimeJobState = "running"
	CommandRuntimeJobStopping    CommandRuntimeJobState = "stopping"
	CommandRuntimeJobCompleted   CommandRuntimeJobState = "completed"
	CommandRuntimeJobFailed      CommandRuntimeJobState = "failed"
	CommandRuntimeJobTimedOut    CommandRuntimeJobState = "timed_out"
	CommandRuntimeJobCancelled   CommandRuntimeJobState = "cancelled"
	CommandRuntimeJobKilled      CommandRuntimeJobState = "killed"
	CommandRuntimeJobInterrupted CommandRuntimeJobState = "interrupted"
)

func (s CommandRuntimeJobState) Valid() bool {
	switch s {
	case CommandRuntimeJobPrepared, CommandRuntimeJobRunning,
		CommandRuntimeJobStopping, CommandRuntimeJobCompleted,
		CommandRuntimeJobFailed, CommandRuntimeJobTimedOut,
		CommandRuntimeJobCancelled, CommandRuntimeJobKilled,
		CommandRuntimeJobInterrupted:
		return true
	default:
		return false
	}
}

func (s CommandRuntimeJobState) Terminal() bool {
	return s == CommandRuntimeJobCompleted || s == CommandRuntimeJobFailed ||
		s == CommandRuntimeJobTimedOut || s == CommandRuntimeJobCancelled ||
		s == CommandRuntimeJobKilled || s == CommandRuntimeJobInterrupted
}

type CommandRuntimeStream string

const (
	CommandRuntimeStdout CommandRuntimeStream = "stdout"
	CommandRuntimeStderr CommandRuntimeStream = "stderr"
)

type CommandRuntimeFrame struct {
	Cursor     uint64               `json:"cursor"`
	NextCursor uint64               `json:"next_cursor"`
	Stream     CommandRuntimeStream `json:"stream"`
	Timestamp  time.Time            `json:"timestamp"`
	Text       string               `json:"text"`
}

type CommandRuntimeOutputPage struct {
	JobID            string                 `json:"job_id"`
	BaseCursor       uint64                 `json:"base_cursor"`
	NextCursor       uint64                 `json:"next_cursor"`
	EndCursor        uint64                 `json:"end_cursor"`
	Frames           []CommandRuntimeFrame  `json:"frames"`
	Dropped          bool                   `json:"dropped"`
	State            CommandRuntimeJobState `json:"state"`
	ExitCode         *int                   `json:"exit_code,omitempty"`
	TruncationReason string                 `json:"truncation_reason,omitempty"`
}

type CommandRuntimeScope struct {
	InvocationID         string
	OperationKey         string
	RunID                string
	MissionID            string
	RootAgentID          string
	SessionID            string
	WorkspaceID          string
	WorkspaceRootSHA256  string
	ModeSnapshotID       string
	ModeRevision         int64
	ProfileSnapshotID    string
	ProfileRevision      int64
	PermissionSnapshotID string
	PermissionRevision   int64
	PermissionMode       domain.RunExecutionPermissionMode
	LeaseID              string
	LeaseGeneration      int64
	OwnerID              string
}

func (s CommandRuntimeScope) Validate() error {
	for _, value := range []string{s.InvocationID, s.OperationKey, s.RunID,
		s.MissionID, s.RootAgentID, s.SessionID, s.WorkspaceID,
		s.ModeSnapshotID, s.ProfileSnapshotID, s.PermissionSnapshotID,
		s.LeaseID, s.OwnerID} {
		if strings.TrimSpace(value) != value || value == "" || !utf8.ValidString(value) ||
			len([]rune(value)) > 256 || strings.ContainsRune(value, 0) {
			return ErrCommandRuntimeBoundary
		}
	}
	if len(s.WorkspaceRootSHA256) != sha256.Size*2 ||
		s.ModeRevision <= 0 || s.ProfileRevision <= 0 ||
		s.PermissionRevision <= 0 || s.LeaseGeneration <= 0 ||
		s.PermissionMode != domain.RunExecutionPermissionFullAccess {
		return ErrCommandRuntimeBoundary
	}
	return nil
}

// CommandRuntimeJob is the durable metadata and sanitized-output projection.
// Initial stdin content and process handles are intentionally absent.
type CommandRuntimeJob struct {
	ID                    string
	OperationDigest       string
	RequestFingerprint    string
	InvocationID          string
	RunID                 string
	MissionID             string
	SessionID             string
	WorkspaceID           string
	RootAgentID           string
	WorkspaceRootSHA256   string
	ModeSnapshotID        string
	ModeRevision          int64
	ProfileSnapshotID     string
	ProfileRevision       int64
	PermissionSnapshotID  string
	PermissionRevision    int64
	PermissionMode        domain.RunExecutionPermissionMode
	LeaseID               string
	LeaseGeneration       int64
	OwnerID               string
	IntentJSON            string
	SpecFingerprint       string
	Profile               CommandRuntimeProfile
	ExecutablePath        string
	ExecutableSHA256      string
	EnvironmentSHA256     string
	WorkingDirectory      string
	StdinPolicy           CommandRuntimeStdinPolicy
	Network               CommandRuntimeNetwork
	TimeoutMilliseconds   int64
	InlineLimitBytes      int
	ArtifactLimitBytes    int
	State                 CommandRuntimeJobState
	PID                   int
	ProcessGroup          int
	Stdout                string
	Stderr                string
	StdoutObservedBytes   int64
	StderrObservedBytes   int64
	OutputCursor          uint64
	TruncationReason      string
	ExitCode              *int
	TimedOut              bool
	Cancelled             bool
	Killed                bool
	TreeReaped            bool
	JobAssignedAtCreation bool
	StdinClosed           bool
	StdinWriteCount       int
	Version               int64
	CreatedAt             time.Time
	StartedAt             *time.Time
	CompletedAt           *time.Time
	UpdatedAt             time.Time
}

func (j CommandRuntimeJob) Validate() error {
	for _, value := range []string{j.ID, j.OperationDigest, j.RequestFingerprint,
		j.InvocationID, j.RunID, j.MissionID, j.SessionID, j.WorkspaceID,
		j.RootAgentID, j.WorkspaceRootSHA256, j.ModeSnapshotID,
		j.ProfileSnapshotID, j.PermissionSnapshotID, j.LeaseID, j.OwnerID,
		j.SpecFingerprint, j.ExecutablePath, j.ExecutableSHA256,
		j.EnvironmentSHA256, j.WorkingDirectory} {
		if strings.TrimSpace(value) != value || value == "" || !utf8.ValidString(value) ||
			strings.ContainsRune(value, 0) {
			return ErrCommandRuntimeBoundary
		}
	}
	for _, digest := range []string{j.OperationDigest, j.RequestFingerprint,
		j.WorkspaceRootSHA256, j.SpecFingerprint, j.ExecutableSHA256,
		j.EnvironmentSHA256} {
		if len(digest) != sha256.Size*2 {
			return ErrCommandRuntimeBoundary
		}
		if decoded, err := hex.DecodeString(digest); err != nil || len(decoded) != sha256.Size {
			return ErrCommandRuntimeBoundary
		}
	}
	if len([]byte(j.IntentJSON)) == 0 || len([]byte(j.IntentJSON)) > MaxCommandRuntimeStoredIntent ||
		!jsonValid(j.IntentJSON) || !j.Profile.Valid() || !j.StdinPolicy.Valid() ||
		j.Network != CommandRuntimeNetworkDisabled || !j.State.Valid() ||
		j.PermissionMode != domain.RunExecutionPermissionFullAccess ||
		j.ModeRevision <= 0 || j.ProfileRevision <= 0 || j.PermissionRevision <= 0 ||
		j.LeaseGeneration <= 0 || j.TimeoutMilliseconds < 1 ||
		j.TimeoutMilliseconds > MaxCommandRuntimeTimeout.Milliseconds() ||
		j.InlineLimitBytes < MinCommandRuntimeInlineBytes ||
		j.InlineLimitBytes > MaxCommandRuntimeInlineBytes ||
		j.ArtifactLimitBytes < j.InlineLimitBytes ||
		j.ArtifactLimitBytes > MaxCommandRuntimeArtifactBytes ||
		!utf8.ValidString(j.Stdout) || !utf8.ValidString(j.Stderr) ||
		len([]byte(j.Stdout)) > j.ArtifactLimitBytes ||
		len([]byte(j.Stderr)) > j.ArtifactLimitBytes ||
		j.StdoutObservedBytes < 0 || j.StderrObservedBytes < 0 ||
		j.StdinWriteCount < 0 || j.StdinWriteCount > MaxCommandRuntimeStdinWrites ||
		j.Version <= 0 || j.CreatedAt.IsZero() || j.UpdatedAt.IsZero() ||
		j.UpdatedAt.Before(j.CreatedAt) {
		return ErrCommandRuntimeBoundary
	}
	if j.State == CommandRuntimeJobPrepared {
		if j.PID != 0 || j.ProcessGroup != 0 || j.StartedAt != nil ||
			j.CompletedAt != nil || j.ExitCode != nil {
			return ErrCommandRuntimeBoundary
		}
	} else if j.StartedAt == nil || j.StartedAt.IsZero() {
		return ErrCommandRuntimeBoundary
	}
	if j.State.Terminal() {
		if j.CompletedAt == nil || j.CompletedAt.Before(*j.StartedAt) ||
			!j.TreeReaped || j.ExitCode == nil {
			return ErrCommandRuntimeBoundary
		}
	} else if j.CompletedAt != nil || j.ExitCode != nil {
		return ErrCommandRuntimeBoundary
	}
	if j.TimedOut != (j.State == CommandRuntimeJobTimedOut) ||
		j.Cancelled != (j.State == CommandRuntimeJobCancelled) ||
		j.Killed != (j.State == CommandRuntimeJobKilled) {
		return ErrCommandRuntimeBoundary
	}
	return nil
}

type CommandRuntimeListFilter struct {
	RunID      string
	Limit      int
	ActiveOnly bool
}

type CommandRuntimeStore interface {
	PrepareCommandRuntimeJob(context.Context, CommandRuntimeJob) (CommandRuntimeJob, bool, error)
	UpdateCommandRuntimeJob(context.Context, CommandRuntimeJob, int64) (CommandRuntimeJob, error)
	GetCommandRuntimeJob(context.Context, string) (CommandRuntimeJob, error)
	ListCommandRuntimeJobs(context.Context, CommandRuntimeListFilter) ([]CommandRuntimeJob, error)
}

type CommandRuntimeStartRequest struct {
	Scope CommandRuntimeScope
	Spec  CommandRuntimeResolvedSpec
}

type CommandRuntimeJobSnapshot struct {
	ID                    string                    `json:"id"`
	State                 CommandRuntimeJobState    `json:"state"`
	Profile               CommandRuntimeProfile     `json:"profile"`
	ExecutablePath        string                    `json:"executable_path"`
	ExecutableSHA256      string                    `json:"executable_sha256"`
	WorkingDirectory      string                    `json:"working_directory"`
	EnvironmentSHA256     string                    `json:"environment_sha256"`
	Network               CommandRuntimeNetwork     `json:"network"`
	PID                   int                       `json:"pid,omitempty"`
	ProcessGroup          int                       `json:"process_group,omitempty"`
	ExitCode              *int                      `json:"exit_code,omitempty"`
	OutputCursor          uint64                    `json:"output_cursor"`
	StdoutObservedBytes   int64                     `json:"stdout_observed_bytes"`
	StderrObservedBytes   int64                     `json:"stderr_observed_bytes"`
	TruncationReason      string                    `json:"truncation_reason,omitempty"`
	TreeReaped            bool                      `json:"tree_reaped"`
	JobAssignedAtCreation bool                      `json:"job_assigned_at_creation"`
	StdinPolicy           CommandRuntimeStdinPolicy `json:"stdin_policy"`
	StdinClosed           bool                      `json:"stdin_closed"`
	Version               int64                     `json:"record_version"`
	CreatedAt             time.Time                 `json:"created_at"`
	StartedAt             *time.Time                `json:"started_at,omitempty"`
	CompletedAt           *time.Time                `json:"completed_at,omitempty"`
}

func ProjectCommandRuntimeJob(job CommandRuntimeJob) CommandRuntimeJobSnapshot {
	return CommandRuntimeJobSnapshot{
		ID: job.ID, State: job.State, Profile: job.Profile,
		ExecutablePath: job.ExecutablePath, ExecutableSHA256: job.ExecutableSHA256,
		WorkingDirectory:  job.WorkingDirectory,
		EnvironmentSHA256: job.EnvironmentSHA256, Network: job.Network,
		PID: job.PID, ProcessGroup: job.ProcessGroup, ExitCode: cloneInt(job.ExitCode),
		OutputCursor: job.OutputCursor, StdoutObservedBytes: job.StdoutObservedBytes,
		StderrObservedBytes: job.StderrObservedBytes,
		TruncationReason:    job.TruncationReason, TreeReaped: job.TreeReaped,
		JobAssignedAtCreation: job.JobAssignedAtCreation,
		StdinPolicy:           job.StdinPolicy, StdinClosed: job.StdinClosed,
		Version: job.Version, CreatedAt: job.CreatedAt,
		StartedAt: cloneTime(job.StartedAt), CompletedAt: cloneTime(job.CompletedAt),
	}
}

type CommandRuntimeProcessOwnership struct {
	PID                   int
	ProcessGroup          int
	JobAssignedAtCreation bool
	KillOnClose           bool
}

type commandRuntimeProcess interface {
	Ownership() CommandRuntimeProcessOwnership
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser
	WriteStdin([]byte) (int, error)
	CloseStdin() error
	Wait() (int, error)
	Cancel(time.Duration) error
	Kill() error
	Close() error
}

type commandRuntimeStarter interface {
	Name() string
	Available() bool
	Start(CommandRuntimeResolvedSpec) (commandRuntimeProcess, error)
}

type CommandRuntimeManager struct {
	store   CommandRuntimeStore
	starter commandRuntimeStarter
	ownerID string

	mu      sync.RWMutex
	entries map[string]*commandRuntimeEntry
	closed  bool
}

type commandRuntimeEntry struct {
	mu         sync.Mutex
	record     CommandRuntimeJob
	process    commandRuntimeProcess
	ring       commandRuntimeRing
	done       chan struct{}
	notify     chan struct{}
	desired    CommandRuntimeJobState
	stdoutDone chan struct{}
	stderrDone chan struct{}
}

func NewCommandRuntimeManager(store CommandRuntimeStore, starter commandRuntimeStarter,
	ownerID string,
) (*CommandRuntimeManager, error) {
	ownerID = strings.TrimSpace(ownerID)
	if store == nil || starter == nil || !starter.Available() ||
		ownerID == "" || !utf8.ValidString(ownerID) || len([]rune(ownerID)) > 256 {
		return nil, ErrCommandRuntimeUnavailable
	}
	return &CommandRuntimeManager{store: store, starter: starter,
		ownerID: ownerID, entries: make(map[string]*commandRuntimeEntry)}, nil
}

func NewPlatformCommandRuntimeManager(store CommandRuntimeStore,
	ownerID string,
) (*CommandRuntimeManager, error) {
	return NewCommandRuntimeManager(store, newPlatformCommandRuntimeStarter(), ownerID)
}

func (m *CommandRuntimeManager) Available() bool {
	return m != nil && m.store != nil && m.starter != nil && m.starter.Available()
}

func (m *CommandRuntimeManager) Start(ctx context.Context,
	request CommandRuntimeStartRequest,
) (CommandRuntimeJobSnapshot, bool, error) {
	if ctx == nil || ctx.Err() != nil || m == nil || !m.Available() ||
		request.Scope.Validate() != nil || request.Scope.OwnerID != m.ownerID {
		return CommandRuntimeJobSnapshot{}, false, ErrCommandRuntimeBoundary
	}
	if request.Spec.Spec.Version != CommandRuntimeProtocolVersion ||
		request.Spec.WorkspaceRootSHA256 != request.Scope.WorkspaceRootSHA256 {
		return CommandRuntimeJobSnapshot{}, false, ErrCommandRuntimeBoundary
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return CommandRuntimeJobSnapshot{}, false, ErrCommandRuntimeUnavailable
	}
	m.mu.Unlock()

	operationDigest := commandRuntimeDigest("command_runtime_operation.v2",
		request.Scope.RunID, request.Scope.OperationKey)
	fingerprint := CommandRuntimeSpecFingerprint(request.Spec)
	jobID := "command-job-" + operationDigest[:24]
	now := time.Now().UTC()
	record := CommandRuntimeJob{
		ID: jobID, OperationDigest: operationDigest,
		RequestFingerprint: commandRuntimeDigest("command_runtime_request.v2",
			request.Scope.RunID, request.Scope.WorkspaceID, fingerprint),
		InvocationID: request.Scope.InvocationID, RunID: request.Scope.RunID,
		MissionID: request.Scope.MissionID, SessionID: request.Scope.SessionID,
		WorkspaceID: request.Scope.WorkspaceID, RootAgentID: request.Scope.RootAgentID,
		WorkspaceRootSHA256: request.Scope.WorkspaceRootSHA256,
		ModeSnapshotID:      request.Scope.ModeSnapshotID, ModeRevision: request.Scope.ModeRevision,
		ProfileSnapshotID:    request.Scope.ProfileSnapshotID,
		ProfileRevision:      request.Scope.ProfileRevision,
		PermissionSnapshotID: request.Scope.PermissionSnapshotID,
		PermissionRevision:   request.Scope.PermissionRevision,
		PermissionMode:       request.Scope.PermissionMode,
		LeaseID:              request.Scope.LeaseID, LeaseGeneration: request.Scope.LeaseGeneration,
		OwnerID: m.ownerID, IntentJSON: commandRuntimeIntentJSON(request.Spec),
		SpecFingerprint: fingerprint, Profile: request.Spec.Spec.Profile,
		ExecutablePath:    request.Spec.ExecutablePath,
		ExecutableSHA256:  request.Spec.ExecutableSHA256,
		EnvironmentSHA256: request.Spec.EnvironmentSHA256,
		WorkingDirectory:  request.Spec.Spec.WorkingDirectory,
		StdinPolicy:       request.Spec.Spec.StdinPolicy, Network: request.Spec.Spec.Network,
		TimeoutMilliseconds: request.Spec.Spec.TimeoutMilliseconds,
		InlineLimitBytes:    request.Spec.Spec.Output.InlineBytes,
		ArtifactLimitBytes:  request.Spec.Spec.Output.ArtifactBytes,
		State:               CommandRuntimeJobPrepared, StdinClosed: request.Spec.Spec.StdinPolicy == CommandRuntimeStdinClosed,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	stored, replayed, err := m.store.PrepareCommandRuntimeJob(ctx, record)
	if err != nil {
		return CommandRuntimeJobSnapshot{}, false, err
	}
	if replayed {
		if stored.RequestFingerprint != record.RequestFingerprint ||
			stored.SpecFingerprint != record.SpecFingerprint {
			return CommandRuntimeJobSnapshot{}, true, ErrCommandRuntimeUncertain
		}
		if stored.State.Terminal() {
			return ProjectCommandRuntimeJob(stored), true, nil
		}
		if entry := m.entry(jobID); entry != nil {
			return entry.snapshot(), true, nil
		}
		return ProjectCommandRuntimeJob(stored), true, ErrCommandRuntimeUncertain
	}

	process, err := m.starter.Start(request.Spec)
	if err != nil {
		failed := stored
		started := now
		completed := time.Now().UTC()
		exitCode := 127
		failed.State = CommandRuntimeJobFailed
		failed.StartedAt = &started
		failed.CompletedAt = &completed
		failed.ExitCode = &exitCode
		failed.TreeReaped = true
		failed.StdinClosed = true
		failed.Stderr = redact.String(err.Error())
		failed.StderrObservedBytes = int64(len([]byte(err.Error())))
		failed.Version++
		failed.UpdatedAt = completed
		updated, updateErr := m.store.UpdateCommandRuntimeJob(context.WithoutCancel(ctx), failed, stored.Version)
		if updateErr == nil {
			stored = updated
		}
		return ProjectCommandRuntimeJob(stored), false, errors.Join(err, updateErr)
	}
	ownership := process.Ownership()
	started := time.Now().UTC()
	running := stored
	running.State = CommandRuntimeJobRunning
	running.PID = ownership.PID
	running.ProcessGroup = ownership.ProcessGroup
	running.JobAssignedAtCreation = ownership.JobAssignedAtCreation
	running.StartedAt = &started
	running.Version++
	running.UpdatedAt = started
	updated, err := m.store.UpdateCommandRuntimeJob(ctx, running, stored.Version)
	if err != nil {
		_ = process.Kill()
		_ = process.Close()
		return CommandRuntimeJobSnapshot{}, false, err
	}
	entry := &commandRuntimeEntry{
		record: updated, process: process,
		ring: commandRuntimeRing{capacity: updated.InlineLimitBytes},
		done: make(chan struct{}), notify: make(chan struct{}, 1),
		stdoutDone: make(chan struct{}), stderrDone: make(chan struct{}),
	}
	m.mu.Lock()
	if m.closed || len(m.entries) >= MaxCommandRuntimeJobsPerRun*4 {
		m.mu.Unlock()
		_ = process.Kill()
		_ = process.Close()
		return CommandRuntimeJobSnapshot{}, false, ErrCommandRuntimeUnavailable
	}
	m.entries[jobID] = entry
	m.mu.Unlock()

	go m.collect(entry, CommandRuntimeStdout, process.Stdout(), entry.stdoutDone)
	go m.collect(entry, CommandRuntimeStderr, process.Stderr(), entry.stderrDone)
	if request.Spec.Spec.StdinPolicy == CommandRuntimeStdinPipe {
		if request.Spec.Spec.InitialStdin != "" {
			if _, err := process.WriteStdin([]byte(request.Spec.Spec.InitialStdin)); err != nil {
				entry.setDesired(CommandRuntimeJobFailed)
				_ = process.Kill()
			}
		}
		if request.Spec.Spec.CloseInitialStdin {
			_ = process.CloseStdin()
			entry.mu.Lock()
			entry.record.StdinClosed = true
			entry.mu.Unlock()
		}
	}
	go m.wait(entry)
	go m.enforceTimeout(entry, time.Duration(updated.TimeoutMilliseconds)*time.Millisecond)
	return entry.snapshot(), false, nil
}

func (m *CommandRuntimeManager) Wait(ctx context.Context, jobID string,
	wait time.Duration, cursor uint64, maxBytes int,
) (CommandRuntimeJobSnapshot, CommandRuntimeOutputPage, error) {
	if wait < 0 || wait > MaxCommandRuntimeWait {
		return CommandRuntimeJobSnapshot{}, CommandRuntimeOutputPage{}, ErrCommandRuntimeBoundary
	}
	entry := m.entry(jobID)
	if entry == nil {
		job, err := m.store.GetCommandRuntimeJob(ctx, strings.TrimSpace(jobID))
		if err != nil {
			return CommandRuntimeJobSnapshot{}, CommandRuntimeOutputPage{}, err
		}
		return ProjectCommandRuntimeJob(job), pageFromStoredJob(job, cursor, maxBytes), nil
	}
	deadline := time.Now().Add(wait)
	for {
		page, err := entry.read(cursor, maxBytes)
		if err != nil {
			return CommandRuntimeJobSnapshot{}, CommandRuntimeOutputPage{}, err
		}
		snapshot := entry.snapshot()
		if len(page.Frames) > 0 || snapshot.State.Terminal() || wait == 0 ||
			time.Now().After(deadline) {
			return snapshot, page, nil
		}
		remaining := time.Until(deadline)
		if remaining > commandRuntimeReadPollInterval {
			remaining = commandRuntimeReadPollInterval
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			timer.Stop()
			return CommandRuntimeJobSnapshot{}, CommandRuntimeOutputPage{}, ctx.Err()
		case <-entry.notify:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (m *CommandRuntimeManager) WriteStdin(ctx context.Context, jobID string,
	data []byte, closeAfter bool,
) (CommandRuntimeJobSnapshot, int, error) {
	if ctx == nil || ctx.Err() != nil || len(data) > MaxCommandRuntimeStdinBytes ||
		!utf8.Valid(data) || strings.ContainsRune(string(data), 0) ||
		redact.String(string(data)) != string(data) {
		return CommandRuntimeJobSnapshot{}, 0, ErrCommandRuntimeBoundary
	}
	entry := m.entry(jobID)
	if entry == nil {
		return CommandRuntimeJobSnapshot{}, 0, ErrCommandRuntimeJobNotFound
	}
	entry.mu.Lock()
	if entry.record.State != CommandRuntimeJobRunning ||
		entry.record.StdinPolicy != CommandRuntimeStdinPipe ||
		entry.record.StdinClosed || entry.record.StdinWriteCount >= MaxCommandRuntimeStdinWrites {
		entry.mu.Unlock()
		return CommandRuntimeJobSnapshot{}, 0, ErrCommandRuntimeJobClosed
	}
	process := entry.process
	entry.mu.Unlock()
	written := 0
	var err error
	if len(data) > 0 {
		written, err = process.WriteStdin(data)
	}
	if err == nil && closeAfter {
		err = process.CloseStdin()
	}
	entry.mu.Lock()
	entry.record.StdinWriteCount++
	entry.record.StdinClosed = closeAfter || entry.record.StdinClosed
	entry.mu.Unlock()
	return entry.snapshot(), written, err
}

func (m *CommandRuntimeManager) Stop(ctx context.Context, jobID string,
	kill bool, grace time.Duration,
) (CommandRuntimeJobSnapshot, error) {
	if ctx == nil || ctx.Err() != nil || grace < 0 || grace > MaxCommandRuntimeCancelGrace {
		return CommandRuntimeJobSnapshot{}, ErrCommandRuntimeBoundary
	}
	entry := m.entry(jobID)
	if entry == nil {
		job, err := m.store.GetCommandRuntimeJob(ctx, strings.TrimSpace(jobID))
		if err != nil {
			return CommandRuntimeJobSnapshot{}, err
		}
		if job.State.Terminal() {
			return ProjectCommandRuntimeJob(job), nil
		}
		return ProjectCommandRuntimeJob(job), ErrCommandRuntimeUncertain
	}
	desired := CommandRuntimeJobCancelled
	if kill {
		desired = CommandRuntimeJobKilled
	}
	entry.setDesired(desired)
	var err error
	if kill {
		err = entry.process.Kill()
	} else {
		err = entry.process.Cancel(grace)
	}
	return entry.snapshot(), err
}

func (m *CommandRuntimeManager) Get(ctx context.Context,
	jobID string,
) (CommandRuntimeJobSnapshot, error) {
	if entry := m.entry(jobID); entry != nil {
		return entry.snapshot(), nil
	}
	job, err := m.store.GetCommandRuntimeJob(ctx, strings.TrimSpace(jobID))
	return ProjectCommandRuntimeJob(job), err
}

func (m *CommandRuntimeManager) List(ctx context.Context,
	filter CommandRuntimeListFilter,
) ([]CommandRuntimeJobSnapshot, error) {
	jobs, err := m.store.ListCommandRuntimeJobs(ctx, filter)
	if err != nil {
		return nil, err
	}
	result := make([]CommandRuntimeJobSnapshot, len(jobs))
	for index, job := range jobs {
		if entry := m.entry(job.ID); entry != nil {
			result[index] = entry.snapshot()
		} else {
			result[index] = ProjectCommandRuntimeJob(job)
		}
	}
	return result, nil
}

func (m *CommandRuntimeManager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	entries := make([]*commandRuntimeEntry, 0, len(m.entries))
	for _, entry := range m.entries {
		entries = append(entries, entry)
	}
	m.mu.Unlock()
	var result error
	for _, entry := range entries {
		entry.setDesired(CommandRuntimeJobInterrupted)
		result = errors.Join(result, entry.process.Kill())
	}
	for _, entry := range entries {
		select {
		case <-entry.done:
		case <-ctx.Done():
			return errors.Join(result, ctx.Err())
		}
	}
	return result
}

func (m *CommandRuntimeManager) ReconcileStartup(ctx context.Context) (int, error) {
	if m == nil || m.store == nil {
		return 0, ErrCommandRuntimeUnavailable
	}
	jobs, err := m.store.ListCommandRuntimeJobs(ctx, CommandRuntimeListFilter{ActiveOnly: true, Limit: 500})
	if err != nil {
		return 0, err
	}
	count := 0
	for _, job := range jobs {
		if m.entry(job.ID) != nil || job.State.Terminal() {
			continue
		}
		cleanupCommandRuntimeOrphan(job.PID, job.ProcessGroup)
		now := time.Now().UTC()
		exitCode := 125
		previous := job.Version
		if job.StartedAt == nil {
			started := job.CreatedAt
			job.StartedAt = &started
		}
		job.State = CommandRuntimeJobInterrupted
		job.CompletedAt = &now
		job.ExitCode = &exitCode
		job.TreeReaped = true
		job.StdinClosed = true
		job.Version++
		job.UpdatedAt = now
		if _, updateErr := m.store.UpdateCommandRuntimeJob(ctx, job, previous); updateErr != nil {
			return count, updateErr
		}
		count++
	}
	return count, nil
}

func (m *CommandRuntimeManager) collect(entry *commandRuntimeEntry,
	stream CommandRuntimeStream, reader io.ReadCloser, done chan struct{},
) {
	defer close(done)
	if reader == nil {
		return
	}
	defer reader.Close()
	sanitizer := &outputsafe.Stream{}
	buffer := make([]byte, 16*1024)
	pending := ""
	flush := func(force bool) {
		for {
			index := strings.IndexByte(pending, '\n')
			if index < 0 && !force && len([]byte(pending)) < 64*1024 {
				return
			}
			end := len(pending)
			if index >= 0 {
				end = index + 1
			} else if !force && end > 32*1024 {
				end = 32 * 1024
			}
			if end == 0 {
				return
			}
			entry.appendOutput(stream, redact.String(pending[:end]), time.Now().UTC())
			pending = pending[end:]
			if pending == "" || force && index < 0 {
				return
			}
		}
	}
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			entry.observe(stream, count)
			pending += sanitizer.Feed(buffer[:count])
			flush(false)
		}
		if err != nil {
			pending += sanitizer.Flush()
			flush(true)
			return
		}
	}
}

func (m *CommandRuntimeManager) wait(entry *commandRuntimeEntry) {
	exitCode, waitErr := entry.process.Wait()
	_ = entry.process.CloseStdin()
	<-entry.stdoutDone
	<-entry.stderrDone
	now := time.Now().UTC()
	entry.mu.Lock()
	desired := entry.desired
	state := desired
	if state == "" || state == CommandRuntimeJobStopping {
		if waitErr != nil || exitCode != 0 {
			state = CommandRuntimeJobFailed
		} else {
			state = CommandRuntimeJobCompleted
		}
	}
	previous := entry.record.Version
	entry.record.State = state
	entry.record.ExitCode = &exitCode
	entry.record.CompletedAt = &now
	entry.record.UpdatedAt = now
	entry.record.Version++
	entry.record.TreeReaped = true
	entry.record.StdinClosed = true
	entry.record.TimedOut = state == CommandRuntimeJobTimedOut
	entry.record.Cancelled = state == CommandRuntimeJobCancelled
	entry.record.Killed = state == CommandRuntimeJobKilled
	record := entry.record
	entry.mu.Unlock()
	if updated, err := m.store.UpdateCommandRuntimeJob(context.Background(), record, previous); err == nil {
		entry.mu.Lock()
		entry.record = updated
		entry.mu.Unlock()
	}
	_ = entry.process.Close()
	entry.signal()
	close(entry.done)
}

func (m *CommandRuntimeManager) enforceTimeout(entry *commandRuntimeEntry,
	duration time.Duration,
) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-entry.done:
		return
	case <-timer.C:
		entry.setDesired(CommandRuntimeJobTimedOut)
		_ = entry.process.Kill()
	}
}

func (m *CommandRuntimeManager) entry(jobID string) *commandRuntimeEntry {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	entry := m.entries[strings.TrimSpace(jobID)]
	m.mu.RUnlock()
	return entry
}

func (e *commandRuntimeEntry) snapshot() CommandRuntimeJobSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.record.OutputCursor = e.ring.next
	return ProjectCommandRuntimeJob(e.record)
}

func (e *commandRuntimeEntry) setDesired(state CommandRuntimeJobState) {
	e.mu.Lock()
	if !e.record.State.Terminal() {
		e.desired = state
		e.record.State = CommandRuntimeJobStopping
	}
	e.mu.Unlock()
	e.signal()
}

func (e *commandRuntimeEntry) observe(stream CommandRuntimeStream, count int) {
	e.mu.Lock()
	if stream == CommandRuntimeStdout {
		e.record.StdoutObservedBytes += int64(count)
	} else {
		e.record.StderrObservedBytes += int64(count)
	}
	e.mu.Unlock()
}

func (e *commandRuntimeEntry) appendOutput(stream CommandRuntimeStream,
	value string, at time.Time,
) {
	if value == "" {
		return
	}
	e.mu.Lock()
	e.ring.append(stream, value, at)
	target := &e.record.Stdout
	if stream == CommandRuntimeStderr {
		target = &e.record.Stderr
	}
	remaining := e.record.ArtifactLimitBytes - len([]byte(*target))
	if remaining > 0 {
		data := []byte(value)
		if len(data) > remaining {
			data = data[:remaining]
			for len(data) > 0 && !utf8.Valid(data) {
				data = data[:len(data)-1]
			}
			e.record.TruncationReason = "artifact_limit"
		}
		*target += string(data)
	} else {
		e.record.TruncationReason = "artifact_limit"
	}
	e.record.OutputCursor = e.ring.next
	e.mu.Unlock()
	e.signal()
}

func (e *commandRuntimeEntry) read(cursor uint64,
	maxBytes int,
) (CommandRuntimeOutputPage, error) {
	if maxBytes == 0 {
		maxBytes = MaxCommandRuntimeOutputRead
	}
	if maxBytes < 1 || maxBytes > MaxCommandRuntimeOutputRead {
		return CommandRuntimeOutputPage{}, ErrCommandRuntimeBoundary
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	page := e.ring.read(cursor, maxBytes)
	page.JobID = e.record.ID
	page.State = e.record.State
	page.ExitCode = cloneInt(e.record.ExitCode)
	page.TruncationReason = e.record.TruncationReason
	return page, nil
}

func (e *commandRuntimeEntry) signal() {
	select {
	case e.notify <- struct{}{}:
	default:
	}
}

type commandRuntimeRing struct {
	base     uint64
	next     uint64
	bytes    int
	capacity int
	frames   []CommandRuntimeFrame
}

func (r *commandRuntimeRing) append(stream CommandRuntimeStream,
	value string, at time.Time,
) {
	for len(value) > 0 {
		take := len(value)
		if take > 16*1024 {
			take = 16 * 1024
			for take > 0 && !utf8.ValidString(value[:take]) {
				take--
			}
		}
		if take == 0 {
			value = value[1:]
			continue
		}
		part := value[:take]
		frame := CommandRuntimeFrame{Cursor: r.next,
			NextCursor: r.next + uint64(len([]byte(part))), Stream: stream,
			Timestamp: at, Text: part}
		r.next = frame.NextCursor
		r.frames = append(r.frames, frame)
		r.bytes += len([]byte(part))
		value = value[take:]
	}
	for r.bytes > r.capacity && len(r.frames) > 0 {
		overflow := r.bytes - r.capacity
		first := &r.frames[0]
		firstBytes := len([]byte(first.Text))
		if overflow < firstBytes {
			data := []byte(first.Text)
			cut := overflow
			for cut < len(data) && !utf8.Valid(data[cut:]) {
				cut++
			}
			first.Text = string(data[cut:])
			first.Cursor += uint64(cut)
			r.bytes -= cut
			r.base = first.Cursor
			break
		}
		r.bytes -= firstBytes
		r.base = first.NextCursor
		r.frames = r.frames[1:]
	}
}

func (r *commandRuntimeRing) read(cursor uint64,
	maxBytes int,
) CommandRuntimeOutputPage {
	if cursor == 0 {
		cursor = r.base
	}
	dropped := cursor < r.base
	if cursor < r.base {
		cursor = r.base
	}
	if cursor > r.next {
		cursor = r.next
	}
	page := CommandRuntimeOutputPage{BaseCursor: r.base, NextCursor: cursor,
		EndCursor: r.next, Dropped: dropped, Frames: make([]CommandRuntimeFrame, 0)}
	remaining := maxBytes
	for _, frame := range r.frames {
		if frame.NextCursor <= cursor || remaining <= 0 {
			continue
		}
		copyFrame := frame
		if cursor > copyFrame.Cursor {
			cut := int(cursor - copyFrame.Cursor)
			data := []byte(copyFrame.Text)
			if cut > len(data) {
				continue
			}
			for cut < len(data) && !utf8.Valid(data[cut:]) {
				cut++
			}
			copyFrame.Text = string(data[cut:])
			copyFrame.Cursor += uint64(cut)
		}
		data := []byte(copyFrame.Text)
		if len(data) > remaining {
			data = data[:remaining]
			for len(data) > 0 && !utf8.Valid(data) {
				data = data[:len(data)-1]
			}
			copyFrame.Text = string(data)
			copyFrame.NextCursor = copyFrame.Cursor + uint64(len(data))
		}
		if copyFrame.Text == "" {
			break
		}
		page.Frames = append(page.Frames, copyFrame)
		used := len([]byte(copyFrame.Text))
		remaining -= used
		page.NextCursor = copyFrame.NextCursor
		cursor = copyFrame.NextCursor
	}
	return page
}

func pageFromStoredJob(job CommandRuntimeJob, cursor uint64,
	maxBytes int,
) CommandRuntimeOutputPage {
	ring := commandRuntimeRing{capacity: max(job.InlineLimitBytes,
		len([]byte(job.Stdout))+len([]byte(job.Stderr)))}
	if job.Stdout != "" {
		ring.append(CommandRuntimeStdout, job.Stdout, job.UpdatedAt)
	}
	if job.Stderr != "" {
		ring.append(CommandRuntimeStderr, job.Stderr, job.UpdatedAt)
	}
	page := ring.read(cursor, maxBytes)
	page.JobID = job.ID
	page.State = job.State
	page.ExitCode = cloneInt(job.ExitCode)
	page.TruncationReason = job.TruncationReason
	return page
}

func commandRuntimeDigest(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(hash, "%d:", len([]byte(part)))
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{'|'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func jsonValid(value string) bool {
	var target any
	return jsonUnmarshal([]byte(value), &target) == nil
}

// Small indirections keep the runtime model free of an exported JSON helper
// surface while still making validation explicit and testable.
var jsonUnmarshal = func(data []byte, target any) error {
	return json.Unmarshal(data, target)
}
