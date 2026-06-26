package stats

import (
	"sync"
	"testing"
	"time"
)

func TestCounters(t *testing.T) {
	s := New()
	s.IncDone()
	s.IncDone()
	s.IncDone()
	s.IncError()
	s.IncError()
	s.AddBytes(100)
	s.AddBytes(50)
	s.AddInFlight(2)
	s.AddInFlight(-1)

	got := s.Snapshot()
	if got.Done != 3 {
		t.Errorf("Done = %d, want 3", got.Done)
	}
	if got.Errors != 2 {
		t.Errorf("Errors = %d, want 2", got.Errors)
	}
	if got.Bytes != 150 {
		t.Errorf("Bytes = %d, want 150", got.Bytes)
	}
	if got.InFlight != 1 {
		t.Errorf("InFlight = %d, want 1", got.InFlight)
	}
}

func TestSnapshotThroughput(t *testing.T) {
	s := New()
	s.start = time.Now().Add(-2 * time.Second) // simulate 2s elapsed
	for i := 0; i < 10; i++ {
		s.IncDone()
	}
	got := s.Snapshot()
	if got.Elapsed < 2*time.Second {
		t.Errorf("Elapsed = %v, want >= 2s", got.Elapsed)
	}
	// ~10 pages / ~2s ≈ 5/sec; allow slack for scheduling.
	if got.PagesPerSec < 4 || got.PagesPerSec > 6 {
		t.Errorf("PagesPerSec = %.2f, want ~5", got.PagesPerSec)
	}
}

func TestSnapshotZeroElapsedNoDivByZero(t *testing.T) {
	s := New()
	got := s.Snapshot() // basically zero elapsed, zero done
	if got.PagesPerSec != 0 {
		t.Errorf("PagesPerSec = %v, want 0", got.PagesPerSec)
	}
}

func TestConcurrentUpdates(t *testing.T) {
	s := New()
	const goroutines, perG = 50, 200

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				s.IncDone()
				s.AddBytes(1)
			}
		}()
	}
	wg.Wait()

	got := s.Snapshot()
	want := int64(goroutines * perG)
	if got.Done != want {
		t.Errorf("Done = %d, want %d", got.Done, want)
	}
	if got.Bytes != want {
		t.Errorf("Bytes = %d, want %d", got.Bytes, want)
	}
}
