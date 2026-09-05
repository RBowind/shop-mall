package storage

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeResolver is a ReferenceResolver over an explicit set of live keys.
type fakeResolver struct {
	referenced map[string]bool
}

func (f fakeResolver) IsImageKeyReferenced(ctx context.Context, key string) (bool, error) {
	return f.referenced[key], nil
}

type cleanupFixture struct {
	t      *testing.T
	volume *LocalVolume
	ctx    context.Context
	now    time.Time
	logger *slog.Logger
}

func newCleanupFixture(t *testing.T) *cleanupFixture {
	t.Helper()
	return &cleanupFixture{
		t:      t,
		volume: newTestVolume(t, 1<<20),
		ctx:    context.Background(),
		now:    time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func (f *cleanupFixture) put(t *testing.T, key string, content []byte) {
	t.Helper()
	if err := f.volume.Put(f.ctx, key, bytes.NewReader(content), int64(len(content))); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

func (f *cleanupFixture) age(key string, at time.Time) {
	f.t.Helper()
	if err := os.Chtimes(filepath.Join(f.volume.dir, key), at, at); err != nil {
		f.t.Fatalf("age %s: %v", key, err)
	}
}

func TestCleanupDeletesUnreferencedOldObjects(t *testing.T) {
	fix := newCleanupFixture(t)
	referenced := "11111111-1111-1111-1111-111111111111.png"
	orphan := "22222222-2222-2222-2222-222222222222.png"
	fix.put(t, referenced, []byte("referenced"))
	fix.put(t, orphan, []byte("orphan"))
	fix.age(orphan, fix.now.Add(-48*time.Hour))
	resolver := fakeResolver{referenced: map[string]bool{referenced: true}}

	deleted, err := Cleanup(fix.ctx, fix.volume, fix.volume, resolver, 24*time.Hour, fix.now, fix.logger)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	infos, err := fix.volume.ListObjects(fix.ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(infos) != 1 || infos[0].Key != referenced {
		t.Fatalf("objects after cleanup = %+v, want only the referenced key", infos)
	}
}

func TestCleanupKeepsObjectsWithinGracePeriod(t *testing.T) {
	fix := newCleanupFixture(t)
	fix.put(t, "33333333-3333-3333-3333-333333333333.png", []byte("recent-orphan"))
	deleted, err := Cleanup(fix.ctx, fix.volume, fix.volume, fakeResolver{}, 24*time.Hour, fix.now, fix.logger)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0 for a recent orphan", deleted)
	}
}

func TestCleanupKeepsFreshlyTouchedReplacedImage(t *testing.T) {
	fix := newCleanupFixture(t)
	replaced := "44444444-4444-4444-4444-444444444444.png"
	fix.put(t, replaced, []byte("replaced"))
	// The replaced file is old, but the product service touches it at
	// replacement time, so its mtime is now and cleanup must keep it.
	fix.age(replaced, fix.now.Add(-72*time.Hour))
	if err := fix.volume.Touch(fix.ctx, replaced); err != nil {
		t.Fatalf("touch replaced image: %v", err)
	}
	deleted, err := Cleanup(fix.ctx, fix.volume, fix.volume, fakeResolver{}, 24*time.Hour, fix.now, fix.logger)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0 for the freshly touched replaced image", deleted)
	}
}

func TestCleanupRestatsBeforeDeleteSoConcurrentTouchProtectsFile(t *testing.T) {
	fix := newCleanupFixture(t)
	key := "77777777-7777-7777-7777-777777777777.png"
	fix.put(t, key, []byte("replaced"))
	// The file is old, so the enumeration snapshot reports a stale mtime.
	fix.age(key, fix.now.Add(-48*time.Hour))

	// A product transaction replaces the image gallery and Touches dropped keys
	// concurrently with the sweep, after the snapshot was taken. The
	// re-stat before deletion must see the fresh mtime and keep the file.
	if err := fix.volume.Touch(fix.ctx, key); err != nil {
		t.Fatalf("touch: %v", err)
	}
	deleted, err := Cleanup(fix.ctx, fix.volume, fix.volume, fakeResolver{}, 24*time.Hour, fix.now, fix.logger)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 0 {
		t.Fatal("cleanup deleted a file that was Touched after the enumeration snapshot")
	}
	infos, err := fix.volume.ListObjects(fix.ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(infos) != 1 || infos[0].Key != key {
		t.Fatalf("protected file was removed: %+v", infos)
	}
}

func TestCleanupKeepsNonObjectFiles(t *testing.T) {
	fix := newCleanupFixture(t)
	leftover := filepath.Join(fix.volume.dir, "not-a-key.txt")
	if err := os.WriteFile(leftover, []byte("leftover temp"), 0o600); err != nil {
		t.Fatalf("write leftover: %v", err)
	}
	deleted, err := Cleanup(fix.ctx, fix.volume, fix.volume, fakeResolver{}, 0, fix.now, fix.logger)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0 for a non-object file", deleted)
	}
	if _, err := os.Stat(leftover); err != nil {
		t.Fatalf("non-object file was removed: %v", err)
	}
}
