//go:build !windows

package sandbox

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

func sbxAcquireNamespaceNamedLock(appName string) (*sbxNamespaceLock, error) {
	if !sbxAppName.MatchString(appName) {
		return nil, ErrSBXOwnership
	}
	uid := os.Geteuid()
	account, err := user.LookupId(strconv.Itoa(uid))
	if err != nil || account == nil || account.HomeDir == "" || !filepath.IsAbs(account.HomeDir) {
		return nil, errors.Join(ErrSBXOwnership, err)
	}
	// Consult the OS account database, never HOME/XDG/TMP or JournalRoot.
	// All cooperating instances of this account therefore select one inode.
	home, err := filepath.EvalSymlinks(account.HomeDir)
	if err != nil {
		return nil, errors.Join(ErrSBXOwnership, err)
	}
	info, err := os.Stat(home)
	if err != nil || !info.IsDir() || !sbxNamespaceOwnedBy(info, uid) {
		return nil, errors.Join(ErrSBXOwnership, err)
	}
	directory := filepath.Join(home, ".traverse-sbx-locks")
	if err := os.Mkdir(directory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, errors.Join(ErrSBXOwnership, err)
	}
	info, err = os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || !sbxNamespaceOwnedBy(info, uid) {
		return nil, errors.Join(ErrSBXOwnership, err)
	}
	path := filepath.Join(directory, appName+".lock")
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, errors.Join(ErrSBXOwnership, err)
	}
	file := os.NewFile(uintptr(fd), path)
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 ||
		!sbxNamespaceOwnedBy(info, uid) || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		_ = file.Close()
		return nil, errors.Join(ErrSBXOwnership, err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, errors.Join(ErrSBXOwnership, err)
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) {
		_ = file.Close()
		return nil, errors.Join(ErrSBXOwnership, err)
	}
	// Do not unlink the lock: replacing its inode would let a second process
	// lock a new file while this instance still owns the original inode.
	return &sbxNamespaceLock{close: file.Close}, nil
}

func sbxNamespaceOwnedBy(info os.FileInfo, uid int) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(uid)
}
