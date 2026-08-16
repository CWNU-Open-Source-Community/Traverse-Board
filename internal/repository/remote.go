package repository

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/gitmutation"
	"cyberagent-workbench/internal/runmutation"
)

const (
	RemoteProtocolVersion = "repository_remote.v1"
	MaxRemoteHostBytes    = 253
	MaxRemoteTTL          = 15 * time.Minute
	MaxPRTitleRunes       = 256
	MaxPRBodyBytes        = 64 * 1024
)

type RemoteOperation = gitmutation.RemoteOperation

const (
	RemoteFetch      = gitmutation.RemoteFetch
	RemotePullFF     = gitmutation.RemotePullFF
	RemotePushBranch = gitmutation.RemotePushBranch
	RemoteCreatePR   = gitmutation.RemoteCreatePR
	RemoteUpdatePR   = gitmutation.RemoteUpdatePR
)

// RemoteSpec is the structured request. The credential is a NAME reference
// only; the secret never enters this struct, argv, logs, or storage.
type RemoteSpec struct {
	ProtocolVersion  string          `json:"protocol_version"`
	Operation        RemoteOperation `json:"operation"`
	RemoteURL        string          `json:"remote_url"`
	Branch           string          `json:"branch,omitempty"`
	BaseBranch       string          `json:"base_branch,omitempty"`
	PRTitle          string          `json:"pr_title,omitempty"`
	PRBody           string          `json:"pr_body,omitempty"`
	PRNumber         int64           `json:"pr_number,omitempty"`
	CredentialName   string          `json:"credential_name,omitempty"`
	NetworkTTLMillis int64           `json:"network_ttl_millis,omitempty"`
}

// RemoteBinding pins the exact local state and the authorized network scope
// (host/port/protocol/TTL/Run) the operator reviewed.
type RemoteBinding struct {
	ProtocolVersion        string
	RunID                  string
	WorkspaceID            string
	LocalHead              string
	LocalStatusFingerprint string
	RemoteHost             string
	RemotePort             string
	Protocol               string
	Branch                 string
	NetworkTTLMillis       int64
	CredentialName         string
	CapturedAt             time.Time
}

// RemoteReceipt is the post-execution redacted evidence.
type RemoteReceipt struct {
	ProtocolVersion    string
	Operation          RemoteOperation
	PreHead            string
	PostHead           string
	Branch             string
	RemoteHost         string
	CommitID           string
	PullRequestURL     string
	StderrPrefix       string
	ObservedBytes      int
	StartedAt          time.Time
	CompletedAt        time.Time
	BindingFingerprint string
}

// RemoteExecutor runs typed remote operations through the hardened git
// binary or the GitHub API. Credentials are resolved from the store by name
// and handed to git through a temporary askpass helper plus an environment
// variable; they never appear in argv, receipts, or logs.
type RemoteExecutor struct {
	gitPath          string
	credentials      credential.Store
	allowLocalRemote bool // test-only: accepts file:// remotes for fixtures
}

func NewRemoteExecutor(credentials credential.Store) (*RemoteExecutor, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("git binary is unavailable: %w", err)
	}
	return &RemoteExecutor{gitPath: gitPath, credentials: credentials}, nil
}

func (e *RemoteExecutor) Available() bool { return e != nil && e.gitPath != "" }

func (e *RemoteExecutor) ValidateSpec(spec RemoteSpec) error {
	if spec.ProtocolVersion != RemoteProtocolVersion {
		return fmt.Errorf("unsupported remote protocol %q", spec.ProtocolVersion)
	}
	if !spec.Operation.Valid() {
		return fmt.Errorf("unsupported remote operation %q", spec.Operation)
	}
	parsed, err := url.Parse(spec.RemoteURL)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("remote URL must be a clean absolute URL without credentials, query, or fragment")
	}
	if parsed.Scheme != "https" {
		if !(e.allowLocalRemote && parsed.Scheme == "file") {
			return errors.New("remote URL must use HTTPS; local and wrapped protocols are rejected")
		}
	}
	if len(parsed.Hostname()) > MaxRemoteHostBytes || parsed.Hostname() == "" {
		return errors.New("remote host is invalid")
	}
	if parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost" || parsed.Hostname() == "::1" {
		return errors.New("loopback remote hosts are rejected for product use")
	}
	if spec.Branch != "" {
		if err := validateBranchName(spec.Branch); err != nil {
			return err
		}
	}
	if spec.BaseBranch != "" {
		if err := validateBranchName(spec.BaseBranch); err != nil {
			return err
		}
	}
	switch spec.Operation {
	case RemotePushBranch, RemoteCreatePR, RemoteUpdatePR:
		if spec.Branch == "" {
			return errors.New("operation requires a target branch")
		}
		if spec.CredentialName != "" && !credential.ValidName(spec.CredentialName) {
			return errors.New("credential reference is invalid")
		}
	case RemoteFetch, RemotePullFF:
		if spec.Branch == "" {
			return errors.New("operation requires a branch")
		}
	}
	if spec.Operation == RemoteCreatePR || spec.Operation == RemoteUpdatePR {
		if spec.BaseBranch == "" {
			return errors.New("PR operations require a base branch")
		}
		if strings.TrimSpace(spec.PRTitle) == "" || len([]rune(spec.PRTitle)) > MaxPRTitleRunes {
			return errors.New("PR title must be a bounded non-empty value")
		}
		if len(spec.PRBody) > MaxPRBodyBytes || strings.ContainsRune(spec.PRBody, 0) {
			return errors.New("PR body exceeds its bound")
		}
	}
	if spec.NetworkTTLMillis < 1 || spec.NetworkTTLMillis > MaxRemoteTTL.Milliseconds() {
		return errors.New("network TTL must be between 1ms and 15m")
	}
	return nil
}

// ExecuteFetch/PullFF/Push run git with the hardened environment plus
// credential askpass and proxy/ssh/wrapper kill-switches.
func (e *RemoteExecutor) ExecuteGit(ctx context.Context, root string, spec RemoteSpec,
	binding RemoteBinding, operationKey string,
) (RemoteReceipt, error) {
	if e == nil || !e.Available() {
		return RemoteReceipt{}, apperror.New(apperror.CodeFailedPrecondition, "remote executor is unavailable")
	}
	if err := e.ValidateSpec(spec); err != nil {
		return RemoteReceipt{}, err
	}
	if spec.Operation == RemoteCreatePR || spec.Operation == RemoteUpdatePR {
		return RemoteReceipt{}, errors.New("PR operations use the API executor")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(spec.NetworkTTLMillis)*time.Millisecond)
	defer cancel()
	askpass, secret, err := e.askpassHelper(ctx, spec.CredentialName)
	if err != nil {
		return RemoteReceipt{}, err
	}
	defer os.Remove(askpass)
	started := time.Now().UTC()
	var args []string
	switch spec.Operation {
	case RemoteFetch:
		args = []string{"fetch", "--quiet", "--no-tags", spec.RemoteURL, spec.Branch}
	case RemotePullFF:
		args = []string{"pull", "--quiet", "--ff-only", spec.RemoteURL, spec.Branch}
	case RemotePushBranch:
		// New branch only: refuse to touch a branch that already exists.
		exists, err := e.remoteBranchExists(ctx, root, spec)
		if err != nil {
			return RemoteReceipt{}, err
		}
		if exists {
			return RemoteReceipt{}, apperror.New(apperror.CodeFailedPrecondition,
				"target branch already exists on the remote; force push and overwrite are not offered")
		}
		args = []string{"push", "--quiet", spec.RemoteURL, "HEAD:refs/heads/" + spec.Branch}
	}
	command := exec.CommandContext(ctx, e.gitPath, append([]string{"-C", root, "--no-optional-locks",
		"-c", "core.autocrlf=false", "-c", "http.proxy=", "-c", "https.proxy=",
		"-c", "core.sshCommand=", "-c", "protocol.ext.allow=never"}, args...)...)
	command.Dir = root
	command.Env = append(hardenedGitEnvironment(),
		"GIT_ASKPASS="+askpass, "GIT_PASSWORD="+secret)
	var stdout, stderr boundedBuffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	completed := time.Now().UTC()
	receipt := RemoteReceipt{
		ProtocolVersion: RemoteProtocolVersion, Operation: spec.Operation,
		PreHead: binding.LocalHead, Branch: spec.Branch, RemoteHost: binding.RemoteHost,
		StderrPrefix:  redactRemoteOutput(boundedOutput(stderr.String()), secret),
		ObservedBytes: len(stdout.String()) + len(stderr.String()),
		StartedAt:     started, CompletedAt: completed,
		BindingFingerprint: runmutation.Fingerprint("repository_remote_binding.v1", operationKey,
			binding.RemoteHost, binding.RemotePort, binding.Protocol, spec.Branch),
	}
	if ctx.Err() != nil {
		return receipt, ctx.Err()
	}
	if runErr != nil {
		return receipt, apperror.New(apperror.CodeFailedPrecondition,
			"remote git operation failed: "+redactRemoteOutput(boundedOutput(stderr.String()), secret))
	}
	// Post-readback: verify the local HEAD and status are consistent.
	postHead, err := e.currentHead(ctx, root)
	if err != nil {
		return receipt, err
	}
	receipt.PostHead = postHead
	if spec.Operation == RemotePullFF {
		receipt.CommitID = postHead
	}
	return receipt, nil
}

func (e *RemoteExecutor) remoteBranchExists(ctx context.Context, root string, spec RemoteSpec) (bool, error) {
	command := exec.CommandContext(ctx, e.gitPath, "-C", root, "ls-remote", "--heads", spec.RemoteURL, "refs/heads/"+spec.Branch)
	command.Env = hardenedGitEnvironment()
	var stdout, stderr boundedBuffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err != nil {
		return false, apperror.New(apperror.CodeFailedPrecondition, "remote branch probe failed")
	}
	return strings.TrimSpace(stdout.String()) != "", nil
}

func (e *RemoteExecutor) currentHead(ctx context.Context, root string) (string, error) {
	command := exec.CommandContext(ctx, e.gitPath, "-C", root, "rev-parse", "HEAD")
	command.Env = hardenedGitEnvironment()
	var stdout, stderr boundedBuffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", apperror.New(apperror.CodeFailedPrecondition, "post-readback failed")
	}
	return strings.TrimSpace(stdout.String()), nil
}

// askpassHelper materializes the referenced credential for git only: the
// secret travels in a child-process environment variable consumed by a
// temporary helper script, never in argv, logs, or storage.
func (e *RemoteExecutor) askpassHelper(ctx context.Context, credentialName string) (string, string, error) {
	if credentialName == "" || e.credentials == nil || !e.credentials.Available() {
		return "", "", nil
	}
	secret, found, err := e.credentials.Get(ctx, credentialName)
	if err != nil || !found {
		return "", "", apperror.New(apperror.CodeUnavailable, "referenced credential is unavailable")
	}
	digest := runmutation.OperationKeyDigest("remote-askpass", "static", credentialName)[:16]
	var path, script string
	if runtime.GOOS == "windows" {
		path = filepath.Join(os.TempDir(), "askpass-"+digest+".cmd")
		script = "@echo off\r\necho %GIT_PASSWORD%\r\n"
	} else {
		path = filepath.Join(os.TempDir(), "askpass-"+digest+".sh")
		script = "#!/bin/sh\necho \"$GIT_PASSWORD\"\n"
	}
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		return "", "", err
	}
	return path, secret, nil
}

// redactRemoteOutput replaces the referenced secret with a marker if it
// ever leaks into git output.
func redactRemoteOutput(value, secret string) string {
	if secret != "" && strings.Contains(value, secret) {
		return strings.ReplaceAll(value, secret, "[credential-redacted]")
	}
	return value
}
