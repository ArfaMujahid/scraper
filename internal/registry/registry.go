// Package registry is the in-memory store of live job state.
//
// Responsibility: map[OwnerID]map[JobID]*Job guarded by sync.RWMutex
// (read-heavy: SSE + "list my jobs"). Source of truth for running jobs; the
// filesystem is the source of truth for completed ones.
package registry

// TODO: RWMutex-guarded registry with add/get/list/remove operations.
