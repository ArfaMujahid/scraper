package registry

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ArfaMujahid/scraper/internal/job"
)

func newJob(owner job.OwnerID, id job.ID) *job.Job {
	return &job.Job{ID: id, Owner: owner, Status: job.StatusQueued}
}

func TestAddAndGet(t *testing.T) {
	r := New()
	r.Add(newJob("alice", "j1"))

	got, ok := r.Get("alice", "j1")
	if !ok {
		t.Fatal("expected to find j1")
	}
	if got.ID != "j1" || got.Owner != "alice" || got.Status != job.StatusQueued {
		t.Errorf("unexpected job: %+v", got)
	}

	if _, ok := r.Get("alice", "missing"); ok {
		t.Error("expected missing job to be absent")
	}
	if _, ok := r.Get("bob", "j1"); ok {
		t.Error("expected j1 absent for a different owner")
	}
}

func TestGetReturnsSnapshot(t *testing.T) {
	r := New()
	r.Add(newJob("alice", "j1"))

	got, _ := r.Get("alice", "j1")
	got.Status = job.StatusFailed // mutate the returned copy

	again, _ := r.Get("alice", "j1")
	if again.Status != job.StatusQueued {
		t.Errorf("registry job was mutated through a returned snapshot: %v", again.Status)
	}
}

func TestAddStoresCopy(t *testing.T) {
	r := New()
	j := newJob("alice", "j1")
	r.Add(j)
	j.Status = job.StatusFailed // mutate caller's pointer after Add

	got, _ := r.Get("alice", "j1")
	if got.Status != job.StatusQueued {
		t.Errorf("registry aliased the caller's pointer: %v", got.Status)
	}
}

func TestListByOwner(t *testing.T) {
	r := New()
	r.Add(newJob("alice", "j3"))
	r.Add(newJob("alice", "j1"))
	r.Add(newJob("alice", "j2"))
	r.Add(newJob("bob", "b1"))

	got := r.ListByOwner("alice")
	var ids []string
	for _, j := range got {
		ids = append(ids, string(j.ID))
	}
	if diff := cmp.Diff([]string{"j1", "j2", "j3"}, ids); diff != "" {
		t.Errorf("ListByOwner(alice) ids mismatch (-want +got):\n%s", diff)
	}

	if got := r.ListByOwner("nobody"); len(got) != 0 {
		t.Errorf("expected empty list for unknown owner, got %v", got)
	}
}

func TestRemove(t *testing.T) {
	r := New()
	r.Add(newJob("alice", "j1"))
	r.Add(newJob("alice", "j2"))

	r.Remove("alice", "j1")
	if _, ok := r.Get("alice", "j1"); ok {
		t.Error("j1 should be removed")
	}
	if _, ok := r.Get("alice", "j2"); !ok {
		t.Error("j2 should remain")
	}

	// Removing the last job drops the owner bucket.
	r.Remove("alice", "j2")
	if got := r.ListByOwner("alice"); len(got) != 0 {
		t.Errorf("expected alice's bucket gone, got %v", got)
	}
	r.Remove("ghost", "x") // no panic for unknown owner
}

func TestLifecycleTransitions(t *testing.T) {
	r := New()
	r.Add(newJob("alice", "j1"))

	if !r.SetRunning("alice", "j1") {
		t.Fatal("SetRunning returned false")
	}
	got, _ := r.Get("alice", "j1")
	if got.Status != job.StatusRunning || got.StartedAt.IsZero() {
		t.Errorf("after SetRunning: %+v", got)
	}

	if !r.SetDone("alice", "j1") {
		t.Fatal("SetDone returned false")
	}
	got, _ = r.Get("alice", "j1")
	if got.Status != job.StatusDone || got.EndedAt.IsZero() {
		t.Errorf("after SetDone: %+v", got)
	}

	if !r.SetFailed("alice", "j1", errors.New("boom")) {
		t.Fatal("SetFailed returned false")
	}
	got, _ = r.Get("alice", "j1")
	if got.Status != job.StatusFailed || got.Err != "boom" {
		t.Errorf("after SetFailed: %+v", got)
	}

	if r.SetRunning("alice", "missing") {
		t.Error("SetRunning on a missing job should return false")
	}
}

func TestConcurrentAccess(t *testing.T) {
	r := New()
	const owners, perOwner = 8, 50

	var wg sync.WaitGroup
	for o := 0; o < owners; o++ {
		owner := job.OwnerID(string(rune('a' + o)))
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perOwner; i++ {
				id := job.ID(string(rune('a'+o)) + string(rune('0'+i%10)) + string(rune('0'+i/10)))
				r.Add(&job.Job{ID: id, Owner: owner, Status: job.StatusQueued})
				r.SetRunning(owner, id)
				_, _ = r.Get(owner, id)
				_ = r.ListByOwner(owner)
				r.SetDone(owner, id)
			}
		}()
	}
	wg.Wait()

	total := 0
	for o := 0; o < owners; o++ {
		total += len(r.ListByOwner(job.OwnerID(string(rune('a' + o)))))
	}
	if total != owners*perOwner {
		t.Errorf("expected %d jobs total, got %d", owners*perOwner, total)
	}
}
