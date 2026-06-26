// Package registry is the in-memory store of live job state: the source of
// truth for running jobs (the filesystem is the source of truth for completed
// ones). It is read-heavy (SSE + "list my jobs"), so a sync.RWMutex guards a
// per-owner map of jobs.
package registry

import (
	"sort"
	"sync"
	"time"

	"github.com/ArfaMujahid/scraper/internal/job"
)

// Registry holds live jobs per owner, guarded by an RWMutex (reads vastly
// outnumber writes). All access to a stored job happens under the lock, so the
// reads it hands out are snapshots — never the internal pointers.
type Registry struct {
	mu   sync.RWMutex
	jobs map[job.OwnerID]map[job.ID]*job.Job
}

// New creates an empty Registry.
func New() *Registry {
	return &Registry{jobs: make(map[job.OwnerID]map[job.ID]*job.Job)}
}

// Add records a copy of j as a live job for its owner. The registry stores its
// own copy, so the caller's pointer and the registry never alias; subsequent
// changes must go through the Set* methods.
func (r *Registry) Add(j *job.Job) {
	cp := *j
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.jobs[cp.Owner] == nil {
		r.jobs[cp.Owner] = make(map[job.ID]*job.Job)
	}
	r.jobs[cp.Owner][cp.ID] = &cp
}

// Get returns a snapshot of the job, or false if absent. The returned pointer is
// a copy, safe to read without holding any lock.
func (r *Registry) Get(owner job.OwnerID, id job.ID) (*job.Job, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	j, ok := r.jobs[owner][id]
	if !ok {
		return nil, false
	}
	cp := *j
	return &cp, true
}

// ListByOwner returns snapshots of all of an owner's live jobs, sorted by ID for
// a stable order. Each element is a copy, safe to read lock-free.
func (r *Registry) ListByOwner(owner job.OwnerID) []*job.Job {
	r.mu.RLock()
	defer r.mu.RUnlock()
	jobs := r.jobs[owner]
	out := make([]*job.Job, 0, len(jobs))
	for _, j := range jobs {
		cp := *j
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].ID < out[k].ID })
	return out
}

// Remove evicts a job and drops the owner's bucket once it is empty (called on
// completion or by the cleanup janitor).
func (r *Registry) Remove(owner job.OwnerID, id job.ID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	jobs, ok := r.jobs[owner]
	if !ok {
		return
	}
	delete(jobs, id)
	if len(jobs) == 0 {
		delete(r.jobs, owner)
	}
}

// update applies fn to the stored job under the write lock. It returns false if
// the job is absent. fn must be quick — it runs while the lock is held.
func (r *Registry) update(owner job.OwnerID, id job.ID, fn func(*job.Job)) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[owner][id]
	if !ok {
		return false
	}
	fn(j)
	return true
}

// SetRunning marks the job running and stamps StartedAt. Returns false if absent.
func (r *Registry) SetRunning(owner job.OwnerID, id job.ID) bool {
	return r.update(owner, id, func(j *job.Job) {
		j.Status = job.StatusRunning
		j.StartedAt = time.Now()
	})
}

// SetDone marks the job done and stamps EndedAt. Returns false if absent.
func (r *Registry) SetDone(owner job.OwnerID, id job.ID) bool {
	return r.update(owner, id, func(j *job.Job) {
		j.Status = job.StatusDone
		j.EndedAt = time.Now()
	})
}

// SetFailed marks the job failed, stamps EndedAt, and records err. Returns false
// if absent.
func (r *Registry) SetFailed(owner job.OwnerID, id job.ID, err error) bool {
	return r.update(owner, id, func(j *job.Job) {
		j.Status = job.StatusFailed
		j.EndedAt = time.Now()
		if err != nil {
			j.Err = err.Error()
		}
	})
}
