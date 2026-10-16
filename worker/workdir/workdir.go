// Package workdir gives a worker process a private scratch directory, and
// removes the ones left behind by workers that died without cleaning up.
//
// Each process creates a directory under a shared root and holds an
// exclusive flock on it for as long as it runs. The operating system drops
// that lock when the process exits for any reason, including a crash or
// kill -9, so a directory under the root that can be locked belongs to a
// process that is gone. Several workers can share one root.
package workdir

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// openAttempts bounds the retries when another worker starting at the same
// moment removes a new directory before it is locked.
const openAttempts = 5

// Dir is this process's scratch directory.
type Dir struct {
	Path string
	lock *os.File
}

// Open removes abandoned directories under root, then creates and locks a
// new one for this process. It reports how many directories it removed.
func Open(root string) (dir *Dir, removed int, err error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, 0, err
	}
	removed, err = removeAbandoned(root)
	if err != nil {
		return nil, removed, err
	}

	for range openAttempts {
		path, err := os.MkdirTemp(root, "worker-")
		if err != nil {
			return nil, removed, err
		}
		lock, err := os.Open(path)
		if err != nil {
			continue // removed by another worker's cleanup already
		}
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			lock.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				continue // another worker locked it first and is removing it
			}
			return nil, removed, fmt.Errorf("lock %s: %w", path, err)
		}
		// Another worker may have locked and removed the directory between
		// MkdirTemp and Flock; the lock is then on a directory that is gone.
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			lock.Close()
			continue
		}
		return &Dir{Path: path, lock: lock}, removed, nil
	}
	return nil, removed, fmt.Errorf("could not create a scratch directory under %s", root)
}

// Close removes the directory and releases the lock.
func (d *Dir) Close() error {
	err := os.RemoveAll(d.Path)
	if cerr := d.lock.Close(); err == nil {
		err = cerr
	}
	return err
}

// removeAbandoned deletes every directory under root that no running
// process holds a lock on.
func removeAbandoned(root string) (int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name())
		f, err := os.Open(path)
		if err != nil {
			continue // already gone
		}
		// Any error, including EWOULDBLOCK for a live worker, means the
		// directory is left alone.
		if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
			if err := os.RemoveAll(path); err != nil {
				f.Close()
				return removed, fmt.Errorf("remove abandoned %s: %w", path, err)
			}
			removed++
		}
		f.Close()
	}
	return removed, nil
}
