package storage

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

// ReferenceResolver reports whether an object key is still referenced. The
// product service implements it against the products table.
type ReferenceResolver interface {
	IsImageKeyReferenced(ctx context.Context, key string) (bool, error)
}

// Cleanup deletes objects that are no longer referenced by any product and
// have been unreferenced for at least the grace period. The product service
// touches a replaced image at replacement time, so the grace period is
// measured from the moment the file stopped being referenced, not from its
// original upload time.
//
// The grace decision is made against the live disk mtime, not the value from
// the enumeration snapshot. Immediately before deleting, the target is
// re-statted: if a product transaction Touched the file between the snapshot
// and this sweep, the re-stat sees the fresh mtime and the file is kept for
// the remaining grace period.
func Cleanup(ctx context.Context, store Storage, lister ObjectLister, resolver ReferenceResolver, gracePeriod time.Duration, now time.Time, logger *slog.Logger) (int, error) {
	if store == nil || lister == nil || resolver == nil {
		return 0, errors.New("cleanup: storage, lister and resolver are required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	infos, err := lister.ListObjects(ctx)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, info := range infos {
		referenced, err := resolver.IsImageKeyReferenced(ctx, info.Key)
		if err != nil {
			if logger != nil {
				logger.ErrorContext(ctx, "cleanup: resolve reference", "object", redactKey(info.Key), "error", err)
			}
			continue
		}
		if referenced {
			continue
		}
		// Re-stat the target so a concurrent Touch that raced the snapshot
		// still protects the file. A store that cannot re-stat falls back to
		// the snapshot value.
		stater, canStat := store.(ObjectStater)
		if canStat {
			live, err := stater.Stat(ctx, info.Key)
			if err != nil {
				if errors.Is(err, ErrObjectNotFound) {
					continue // already gone, nothing to clean
				}
				if logger != nil {
					logger.ErrorContext(ctx, "cleanup: stat object", "object", redactKey(info.Key), "error", err)
				}
				continue
			}
			info.ModTime = live.ModTime
		}
		if gracePeriod > 0 && now.Sub(info.ModTime) < gracePeriod {
			if logger != nil {
				logger.DebugContext(ctx, "cleanup: keep within grace period", "object", redactKey(info.Key))
			}
			continue
		}
		if err := store.Delete(ctx, info.Key); err != nil {
			if logger != nil {
				logger.ErrorContext(ctx, "cleanup: delete object", "object", redactKey(info.Key), "error", err)
			}
			continue
		}
		deleted++
		if logger != nil {
			logger.InfoContext(ctx, "cleanup: deleted unreferenced object", "object", redactKey(info.Key))
		}
	}
	return deleted, nil
}

// redactKey returns a short prefix of an object key so logs never contain a
// full object key.
func redactKey(key string) string {
	if len(key) <= 8 {
		return "key"
	}
	return "key-" + strings.ToLower(key[:8])
}
