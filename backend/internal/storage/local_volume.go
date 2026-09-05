package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LocalVolume stores image objects as files under a single directory. The
// directory is mounted from the shared images volume (deploy/compose), so
// nginx can serve the same files under /static/images/.
type LocalVolume struct {
	dir      string
	capacity int64
	mu       sync.Mutex
}

// NewLocalVolume creates the volume directory and returns a LocalVolume. The
// directory is world-traversable because a separate nginx container serves the
// same named volume over HTTP.
func NewLocalVolume(dir string, capacity int64) (*LocalVolume, error) {
	if dir == "" {
		return nil, errors.New("local volume: directory is required")
	}
	if capacity <= 0 {
		return nil, errors.New("local volume: capacity must be positive")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("local volume: create directory: %w", err)
	}
	return &LocalVolume{dir: dir, capacity: capacity}, nil
}

// Put writes content under key. The write is atomic: content is copied to a
// temporary file in the same directory and renamed into place, so a partial
// upload is never observable. The capacity check runs under the volume lock
// so concurrent uploads cannot jointly exceed the capacity.
func (v *LocalVolume) Put(ctx context.Context, key string, content io.Reader, size int64) error {
	if v == nil {
		return errors.New("local volume is not configured")
	}
	if !ValidObjectKey(key) {
		return fmt.Errorf("%w", ErrInvalidKey)
	}
	if size < 0 {
		return errors.New("local volume: size must not be negative")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	used, err := v.usedBytesLocked()
	if err != nil {
		return err
	}
	if used+size > v.capacity {
		return ErrCapacityExceeded
	}
	tmp, err := os.CreateTemp(v.dir, ".upload-*")
	if err != nil {
		return fmt.Errorf("local volume: create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	written, err := io.Copy(tmp, io.LimitReader(content, size+1))
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("local volume: write object: %w", err)
	}
	if written != size {
		_ = tmp.Close()
		return errors.New("local volume: content length does not match declared size")
	}
	// The images are public and served by the nginx container from the shared
	// volume, so the stored file must be readable by the nginx user.
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("local volume: set object permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("local volume: close object: %w", err)
	}
	path := v.pathFor(key)
	// Keys are freshly generated UUIDs, so an existing destination can only
	// be a duplicate upload retry. Remove it so rename stays atomic on all
	// platforms.
	if _, statErr := os.Stat(path); statErr == nil {
		if removeErr := os.Remove(path); removeErr != nil {
			return fmt.Errorf("local volume: replace existing object: %w", removeErr)
		}
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("local volume: publish object: %w", err)
	}
	return nil
}

// Delete removes the object identified by key. Deleting a missing object
// returns nil so cleanup can run idempotently.
func (v *LocalVolume) Delete(ctx context.Context, key string) error {
	if v == nil {
		return errors.New("local volume is not configured")
	}
	if !ValidObjectKey(key) {
		return fmt.Errorf("%w", ErrInvalidKey)
	}
	err := os.Remove(v.pathFor(key))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("local volume: delete object: %w", err)
	}
	return nil
}

// ListObjects returns the current objects in the volume. Non-object files
// (for example leftover temporary files) are skipped.
func (v *LocalVolume) ListObjects(ctx context.Context) ([]ObjectInfo, error) {
	if v == nil {
		return nil, errors.New("local volume is not configured")
	}
	entries, err := os.ReadDir(v.dir)
	if err != nil {
		return nil, fmt.Errorf("local volume: list objects: %w", err)
	}
	out := make([]ObjectInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		key := entry.Name()
		if !ValidObjectKey(key) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		out = append(out, ObjectInfo{Key: key, ModTime: info.ModTime(), Size: info.Size()})
	}
	return out, nil
}

// UsedBytes returns the total number of bytes currently stored.
func (v *LocalVolume) UsedBytes(ctx context.Context) (int64, error) {
	if v == nil {
		return 0, errors.New("local volume is not configured")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.usedBytesLocked()
}

func (v *LocalVolume) usedBytesLocked() (int64, error) {
	entries, err := os.ReadDir(v.dir)
	if err != nil {
		return 0, fmt.Errorf("local volume: read directory: %w", err)
	}
	var total int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		total += info.Size()
	}
	return total, nil
}

// CapacityBytes returns the configured hard capacity.
func (v *LocalVolume) CapacityBytes() int64 {
	if v == nil {
		return 0
	}
	return v.capacity
}

// Stat returns the live metadata of a single object. Cleanup uses it to
// re-verify the grace period against the current disk state right before a
// delete, so a Touch that raced the enumeration snapshot still protects the
// object.
func (v *LocalVolume) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	if v == nil {
		return ObjectInfo{}, errors.New("local volume is not configured")
	}
	if !ValidObjectKey(key) {
		return ObjectInfo{}, fmt.Errorf("%w", ErrInvalidKey)
	}
	info, err := os.Stat(v.pathFor(key))
	if err != nil {
		if os.IsNotExist(err) {
			return ObjectInfo{}, ErrObjectNotFound
		}
		return ObjectInfo{}, fmt.Errorf("local volume: stat object: %w", err)
	}
	return ObjectInfo{Key: key, ModTime: info.ModTime(), Size: info.Size()}, nil
}

// Touch extends the mtime of an existing object to now so the cleanup grace
// period restarts. A missing object is a no-op.
func (v *LocalVolume) Touch(ctx context.Context, key string) error {
	if v == nil {
		return errors.New("local volume is not configured")
	}
	if !ValidObjectKey(key) {
		return fmt.Errorf("%w", ErrInvalidKey)
	}
	now := time.Now()
	if err := os.Chtimes(v.pathFor(key), now, now); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("local volume: touch object: %w", err)
	}
	return nil
}

func (v *LocalVolume) pathFor(key string) string {
	return filepath.Join(v.dir, key)
}
