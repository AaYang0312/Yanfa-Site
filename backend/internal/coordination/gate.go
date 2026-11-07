// Package coordination provides the process and data-directory boundaries shared
// by content commits, release activation, response starts and offline writers.
package coordination

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// Gate is shared by the application's content and release services. Take Lock
// around the final workspace transaction/activation; take RLock only while
// checking a release and starting its response, never while streaming it.
// The zero value is ready for use. Do not copy a Gate after first use.
type Gate struct{ sync.RWMutex }

var ErrDataDirLocked = errors.New("DATA_DIR_LOCKED")

// DataDirLock owns a Linux flock until Close. The lock inode is permanent:
// never unlink, replace, or rename .yuanfa-data.lock, even after Close.
type DataDirLock struct {
	mu   sync.Mutex
	file *os.File
	dir  string
}

// NormalizeDataDir resolves existing symlinks after creating the directory.
// Every writer must use the returned canonical directory, including for SQLite.
func NormalizeDataDir(dataDir string) (string, error) {
	if dataDir == "" {
		return "", errors.New("DATA_DIR is required")
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// AcquireDataDir takes LOCK_EX|LOCK_NB before any database/file access. A
// conflicting writer returns ErrDataDirLocked immediately (CLI exit status 73).
func AcquireDataDir(dataDir string) (*DataDirLock, error) {
	dir, err := NormalizeDataDir(dataDir)
	if err != nil {
		return nil, err
	}
	fd, err := syscall.Open(filepath.Join(dir, ".yuanfa-data.lock"), syscall.O_RDWR|syscall.O_CREAT|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), ".yuanfa-data.lock")
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("invalid data directory lock file")
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrDataDirLocked
		}
		return nil, fmt.Errorf("acquire data directory lock: %w", err)
	}
	return &DataDirLock{file: f, dir: dir}, nil
}

// Dir returns the canonical DATA_DIR, or an error if the lock has been closed.
// The owner must not Close concurrently with users of this path or their DBs.
func (l *DataDirLock) Dir() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return "", errors.New("data directory lock is closed")
	}
	return l.dir, nil
}

// Close releases the lock only after HTTP, child processes and SQLite have
// stopped. It is idempotent and leaves the lock file and its inode intact.
func (l *DataDirLock) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
