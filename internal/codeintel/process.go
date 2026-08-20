package codeintel

import (
	"context"
	"errors"
	"os/exec"
	"sync"
)

type processTree interface {
	Kill() error
	Close() error
}

type ownedCommand struct {
	cmd      *exec.Cmd
	tree     processTree
	done     chan struct{}
	waitOnce sync.Once
	waitMu   sync.Mutex
	waitErr  error
}

func startOwnedCommand(cmd *exec.Cmd) (*ownedCommand, error) {
	if cmd == nil {
		return nil, errors.New("LSP command is required")
	}
	prepareOwnedCommand(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	tree, err := attachOwnedProcessTree(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	owned := &ownedCommand{cmd: cmd, tree: tree, done: make(chan struct{})}
	go owned.wait()
	return owned, nil
}

func (p *ownedCommand) wait() {
	p.waitOnce.Do(func() {
		err := p.cmd.Wait()
		p.waitMu.Lock()
		p.waitErr = err
		p.waitMu.Unlock()
		if p.tree != nil {
			_ = p.tree.Close()
		}
		close(p.done)
	})
}

func (p *ownedCommand) Kill() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	if p.tree != nil {
		return p.tree.Kill()
	}
	return p.cmd.Process.Kill()
}

func (p *ownedCommand) Err() error {
	if p == nil {
		return errors.New("LSP process is unavailable")
	}
	p.waitMu.Lock()
	defer p.waitMu.Unlock()
	return p.waitErr
}

func (p *ownedCommand) Wait(ctx context.Context) error {
	if p == nil || p.done == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
