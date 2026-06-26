// Package cleanup is the background janitor: it deletes completed job output
// past the retention window and evicts the corresponding registry entries.
//
// It is deliberately defensive (NFR-R4): it touches only the owned scrapes
// directory, only files with our own extensions, never a running job's file,
// and a failed delete is logged and skipped — never fatal.
package cleanup

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ArfaMujahid/scraper/internal/job"
	"github.com/ArfaMujahid/scraper/internal/registry"
)

// outputExts are the only file extensions the janitor will ever delete.
var outputExts = map[string]bool{".jsonl": true, ".csv": true}

// Janitor periodically deletes completed job output older than retention.
type Janitor struct {
	dir       string // the scrapes root (job.ScrapesDir) — the only dir it touches
	retention time.Duration
	interval  time.Duration
	registry  *registry.Registry
	logger    *slog.Logger
}

// New builds a Janitor scoped to exactly one scrapes directory.
func New(dir string, retention, interval time.Duration, reg *registry.Registry, log *slog.Logger) *Janitor {
	if log == nil {
		log = slog.Default()
	}
	return &Janitor{dir: dir, retention: retention, interval: interval, registry: reg, logger: log}
}

// Run sweeps once immediately, then on every tick, until ctx is cancelled.
func (jn *Janitor) Run(ctx context.Context) {
	jn.sweep()
	ticker := time.NewTicker(jn.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			jn.sweep()
		}
	}
}

// sweep deletes eligible files across all owner directories once.
func (jn *Janitor) sweep() {
	cutoff := time.Now().Add(-jn.retention)
	owners, err := os.ReadDir(jn.dir)
	if err != nil {
		if !os.IsNotExist(err) {
			jn.logger.Warn("cleanup: reading scrapes dir", "dir", jn.dir, "err", err)
		}
		return
	}
	for _, ownerEntry := range owners {
		if ownerEntry.IsDir() {
			jn.sweepOwner(ownerEntry.Name(), cutoff)
		}
	}
}

// sweepOwner deletes eligible files in one owner directory and removes the
// directory if it ends up empty.
func (jn *Janitor) sweepOwner(owner string, cutoff time.Time) {
	ownerDir := filepath.Join(jn.dir, owner)
	files, err := os.ReadDir(ownerDir)
	if err != nil {
		jn.logger.Warn("cleanup: reading owner dir", "dir", ownerDir, "err", err)
		return
	}
	for _, f := range files {
		if f.IsDir() || !outputExts[strings.ToLower(filepath.Ext(f.Name()))] {
			continue // only our own output files, ever
		}
		info, err := f.Info()
		if err != nil {
			jn.logger.Warn("cleanup: stat", "file", f.Name(), "err", err)
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue // not old enough — also keeps freshly-written running jobs
		}
		id := job.ID(strings.SplitN(f.Name(), "_", 2)[0])
		if jn.isActive(job.OwnerID(owner), id) {
			continue // never delete a running/queued job's file
		}
		path := filepath.Join(ownerDir, f.Name())
		if err := os.Remove(path); err != nil {
			jn.logger.Warn("cleanup: removing file", "file", path, "err", err)
			continue
		}
		jn.logger.Info("cleanup: removed old job output", "file", path)
		jn.registry.Remove(job.OwnerID(owner), id)
	}
	jn.removeIfEmpty(ownerDir)
}

// isActive reports whether the registry still holds this job as running/queued,
// so its file must be protected even if old.
func (jn *Janitor) isActive(owner job.OwnerID, id job.ID) bool {
	if jn.registry == nil {
		return false
	}
	j, ok := jn.registry.Get(owner, id)
	return ok && (j.Status == job.StatusRunning || j.Status == job.StatusQueued)
}

// removeIfEmpty deletes dir when it has no remaining entries.
func (jn *Janitor) removeIfEmpty(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	if len(entries) == 0 {
		if err := os.Remove(dir); err != nil {
			jn.logger.Warn("cleanup: removing empty owner dir", "dir", dir, "err", err)
		}
	}
}
