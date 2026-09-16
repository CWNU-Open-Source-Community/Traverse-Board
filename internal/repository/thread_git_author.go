package repository

import (
	"context"
	"errors"
	"net/mail"
	"os"
	"os/exec"
	"strings"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
)

// GitCommitAuthor is the only Git configuration projected into an explicit
// selected commit. The same reviewed identity is used for author and committer.
type GitCommitAuthor struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func validGitCommitAuthor(a GitCommitAuthor) bool {
	if !utf8.ValidString(a.Name) || !utf8.ValidString(a.Email) || a.Name == "" || len([]rune(a.Name)) > 256 || len(a.Email) > 254 || a.Email == "" {
		return false
	}
	for _, value := range []string{a.Name, a.Email} {
		if strings.TrimSpace(value) != value || strings.ContainsAny(value, "<>") || strings.ContainsFunc(value, unicode.IsControl) {
			return false
		}
	}
	parsed, err := mail.ParseAddress(a.Email)
	return err == nil && parsed.Address == a.Email && parsed.Name == ""
}

// ReadCommitAuthor reads only two inert scalar keys. Repository/worktree config
// takes precedence; global config is queried separately and never inherited by
// mutation processes. Git itself resolves include/includeIf identities; this
// config-only command never dispatches hooks or helpers and returns no other
// settings. No identity is guessed from the OS login or hostname.
func (e *MutationExecutor) ReadCommitAuthor(ctx context.Context, root string) (GitCommitAuthor, error) {
	var author GitCommitAuthor
	values := []*string{&author.Name, &author.Email}
	for i, key := range []string{"user.name", "user.email"} {
		value, found, err := e.readIdentityConfig(ctx, root, key, false)
		if err != nil {
			return author, err
		}
		if !found {
			value, found, err = e.readIdentityConfig(ctx, root, key, true)
			if err != nil {
				return author, err
			}
		}
		if !found {
			return author, apperror.New(apperror.CodeFailedPrecondition, "Git 提交身份不完整；请先在仓库或全局 Git 配置中设置 user.name 和 user.email")
		}
		*values[i] = strings.TrimSpace(value)
	}
	if !validGitCommitAuthor(author) {
		return GitCommitAuthor{}, apperror.New(apperror.CodeFailedPrecondition, "Git 提交姓名或邮箱无效；请检查 user.name 和 user.email")
	}
	return author, nil
}

func (e *MutationExecutor) readIdentityConfig(ctx context.Context, root, key string, global bool) (string, bool, error) {
	if key != "user.name" && key != "user.email" {
		return "", false, errors.New("unsupported Git identity key")
	}
	ctx, cancel := context.WithTimeout(ctx, MaxGitDuration)
	defer cancel()
	args := []string{"-C", root, "--no-optional-locks", "config", "--includes"}
	if global {
		args = append(args, "--global")
	}
	args = append(args, "--get", key)
	command := e.commandContext
	if command == nil {
		command = repositoryCommandContext
	}
	cmd := command(ctx, e.gitPath, args...)
	cmd.Dir = root
	cmd.Env = hardenedGitEnvironment()
	if value := os.Getenv("XDG_CONFIG_HOME"); value != "" {
		cmd.Env = append(cmd.Env, "XDG_CONFIG_HOME="+value)
	}
	if global {
		// This process can only read the selected scalar; no global executable
		// setting, credential helper, signing configuration or hook is executed.
		filtered := cmd.Env[:0]
		for _, value := range cmd.Env {
			if !strings.HasPrefix(value, "GIT_CONFIG_GLOBAL=") {
				filtered = append(filtered, value)
			}
		}
		cmd.Env = filtered
		if value := os.Getenv("GIT_CONFIG_GLOBAL"); value != "" {
			cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL="+value)
		}
	}
	var stdout, stderr boundedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", false, nil
		}
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		return "", false, apperror.New(apperror.CodeFailedPrecondition, "无法读取 Git 提交身份；请检查仓库和全局身份配置")
	}
	if stdout.buf.Len() >= MaxGitOutputBytes {
		return "", false, apperror.New(apperror.CodeResourceExhausted, "Git 提交身份超出读取上限")
	}
	return strings.TrimSuffix(stdout.String(), "\n"), true, nil
}
