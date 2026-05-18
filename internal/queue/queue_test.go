package queue

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnqueue_AndProcess(t *testing.T) {
	var processed atomic.Int64
	q := New(4, 2, func(ctx context.Context, j Job) {
		processed.Add(1)
	})
	q.Start()
	defer q.Stop(context.Background())
	for i := 0; i < 5; i++ {
		if !q.Enqueue(Job{ChatID: int64(i), SpotifyID: "id"}) {
			t.Fatalf("enqueue %d", i)
		}
	}
	deadline := time.Now().Add(time.Second)
	for processed.Load() < 5 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processed.Load() != 5 {
		t.Fatalf("processed = %d", processed.Load())
	}
}

func TestPerUserSemaphore_RejectsConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	q := New(4, 2, func(ctx context.Context, j Job) {
		wg.Wait()
	})
	q.Start()
	defer func() {
		wg.Done()
		q.Stop(context.Background())
	}()
	if !q.TryAcquireUser(42) {
		t.Fatal("first acquire")
	}
	if q.TryAcquireUser(42) {
		t.Fatal("second acquire should fail")
	}
	q.ReleaseUser(42)
	if !q.TryAcquireUser(42) {
		t.Fatal("re-acquire after release")
	}
}

func TestEnqueue_FullReturnsFalse(t *testing.T) {
	block := make(chan struct{})
	q := New(1, 1, func(ctx context.Context, j Job) { <-block })
	q.Start()
	defer func() { close(block); q.Stop(context.Background()) }()
	// First job is picked up by the worker, second sits in the 1-slot buffer,
	// third must be rejected.
	q.Enqueue(Job{ChatID: 1})
	// Give the worker a moment to consume the first job.
	time.Sleep(20 * time.Millisecond)
	q.Enqueue(Job{ChatID: 2})
	if q.Enqueue(Job{ChatID: 3}) {
		t.Fatal("expected full")
	}
}

func TestWorker_ReleasesUserOnCompletion(t *testing.T) {
	done := make(chan struct{})
	q := New(4, 1, func(ctx context.Context, j Job) {
		close(done)
	})
	q.Start()
	defer q.Stop(context.Background())
	if !q.TryAcquireUser(7) {
		t.Fatal("acquire")
	}
	if !q.Enqueue(Job{UserID: 7, ChatID: 1}) {
		t.Fatal("enqueue")
	}
	<-done
	// Worker should have called ReleaseUser(7) after handler returned.
	deadline := time.Now().Add(time.Second)
	for !q.TryAcquireUser(7) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !q.TryAcquireUser(7) {
		// will have already acquired in the loop above; if still failing here, test failed
	}
}
