package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newTestVolume(t *testing.T, capacity int64) *LocalVolume {
	t.Helper()
	volume, err := NewLocalVolume(t.TempDir(), capacity)
	if err != nil {
		t.Fatalf("new local volume: %v", err)
	}
	return volume
}

func TestLocalVolumePutReadAndDeleteRoundTrip(t *testing.T) {
	volume := newTestVolume(t, 1<<20)
	ctx := context.Background()
	key := "01234567-89ab-cdef-0123-456789abcdef.png"
	content := []byte("fake-but-valid-object-content")

	if err := volume.Put(ctx, key, bytes.NewReader(content), int64(len(content))); err != nil {
		t.Fatalf("put: %v", err)
	}
	path := filepath.Join(volume.dir, key)
	read, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stored object: %v", err)
	}
	if !bytes.Equal(read, content) {
		t.Fatalf("stored content mismatch: got %q", read)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat stored object: %v", err)
	}
	// The nginx container serves the same volume, so the file must be
	// world-readable.
	if perm := info.Mode().Perm(); perm&0o004 == 0 {
		t.Fatalf("stored object mode %o is not world-readable", perm)
	}
	used, err := volume.UsedBytes(ctx)
	if err != nil {
		t.Fatalf("used bytes: %v", err)
	}
	if used != int64(len(content)) {
		t.Fatalf("used = %d, want %d", used, len(content))
	}
	infos, err := volume.ListObjects(ctx)
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}
	if len(infos) != 1 || infos[0].Key != key {
		t.Fatalf("list objects = %+v, want exactly %q", infos, key)
	}
	if err := volume.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("object still present after delete: %v", err)
	}
	// Deleting a missing object is idempotent.
	if err := volume.Delete(ctx, key); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}

func TestLocalVolumePutRejectsUntrustedKeys(t *testing.T) {
	volume := newTestVolume(t, 1<<20)
	ctx := context.Background()
	for _, key := range []string{
		"",
		"../../tmp/evil.jpg",
		"/absolute/path.jpg",
		"a/b/01234567-89ab-cdef-0123-456789abcdef.jpg",
	} {
		if err := volume.Put(ctx, key, bytes.NewReader([]byte("x")), 1); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("put %q error = %v, want ErrInvalidKey", key, err)
		}
	}
	if err := volume.Delete(ctx, "../etc/passwd"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("delete traversal error = %v, want ErrInvalidKey", err)
	}
}

func TestLocalVolumePutRejectsContentLengthMismatch(t *testing.T) {
	volume := newTestVolume(t, 1<<20)
	key := "01234567-89ab-cdef-0123-456789abcdef.png"
	// Declared size 10 but only 3 bytes are readable.
	if err := volume.Put(context.Background(), key, bytes.NewReader([]byte("abc")), 10); err == nil {
		t.Fatal("expected content length mismatch error")
	}
}

func TestLocalVolumePutEnforcesCapacity(t *testing.T) {
	volume := newTestVolume(t, 10)
	ctx := context.Background()
	key := "01234567-89ab-cdef-0123-456789abcdef.png"
	if err := volume.Put(ctx, key, bytes.NewReader(make([]byte, 6)), 6); err != nil {
		t.Fatalf("first put: %v", err)
	}
	// 6 + 5 > 10: the second object must be rejected as capacity exceeded.
	other := "11234567-89ab-cdef-0123-456789abcdef.png"
	if err := volume.Put(ctx, other, bytes.NewReader(make([]byte, 5)), 5); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("capacity error = %v, want ErrCapacityExceeded", err)
	}
	if infos, _ := volume.ListObjects(ctx); len(infos) != 1 {
		t.Fatalf("partial write recorded: %+v", infos)
	}
}

func TestLocalVolumePutOverwritesExistingKeyAtomically(t *testing.T) {
	volume := newTestVolume(t, 1<<20)
	ctx := context.Background()
	key := "01234567-89ab-cdef-0123-456789abcdef.png"
	first := []byte("first")
	if err := volume.Put(ctx, key, bytes.NewReader(first), int64(len(first))); err != nil {
		t.Fatalf("first put: %v", err)
	}
	second := []byte("second-content")
	if err := volume.Put(ctx, key, bytes.NewReader(second), int64(len(second))); err != nil {
		t.Fatalf("second put: %v", err)
	}
	read, err := os.ReadFile(filepath.Join(volume.dir, key))
	if err != nil {
		t.Fatalf("read after overwrite: %v", err)
	}
	if !bytes.Equal(read, second) {
		t.Fatalf("overwrite kept stale content: %q", read)
	}
}

func TestLocalVolumeTouchExtendsModTime(t *testing.T) {
	volume := newTestVolume(t, 1<<20)
	ctx := context.Background()
	key := "01234567-89ab-cdef-0123-456789abcdef.png"
	content := []byte("touch-me")
	if err := volume.Put(ctx, key, bytes.NewReader(content), int64(len(content))); err != nil {
		t.Fatalf("put: %v", err)
	}
	infos, err := volume.ListObjects(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	before := infos[0].ModTime
	if err := volume.Touch(ctx, key); err != nil {
		t.Fatalf("touch: %v", err)
	}
	infos, err = volume.ListObjects(ctx)
	if err != nil {
		t.Fatalf("list after touch: %v", err)
	}
	if infos[0].ModTime.Before(before) {
		t.Fatalf("touch moved mtime backwards: before=%v after=%v", before, infos[0].ModTime)
	}
	if err := volume.Touch(ctx, "99999999-0000-0000-0000-000000000000.png"); err != nil {
		t.Fatalf("touch missing object: %v", err)
	}
}

func TestLocalVolumeCapacityBytes(t *testing.T) {
	volume := newTestVolume(t, 12345)
	if volume.CapacityBytes() != 12345 {
		t.Fatalf("capacity = %d, want 12345", volume.CapacityBytes())
	}
}
