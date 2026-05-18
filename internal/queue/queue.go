package queue

import (
	"context"
	"sync"
)

// Job is the unit of work dispatched from the bot handler.
type Job struct {
	ChatID         int64
	UserID         int64
	SpotifyURL     string
	SpotifyID      string
	ReplyMessageID int
}

// Handler processes a Job. It must respect ctx cancellation.
type Handler func(ctx context.Context, j Job)

type Queue struct {
	ch       chan Job
	workers  int
	handler  Handler
	wg       sync.WaitGroup
	cancel   context.CancelFunc
	rootCtx  context.Context
	stopOnce sync.Once

	mu    sync.Mutex
	locks map[int64]struct{}
}

func New(buffer, workers int, h Handler) *Queue {
	return &Queue{
		ch:      make(chan Job, buffer),
		workers: workers,
		handler: h,
		locks:   make(map[int64]struct{}),
	}
}

func (q *Queue) Start() {
	q.rootCtx, q.cancel = context.WithCancel(context.Background())
	ready := make(chan struct{}, q.workers)
	for i := 0; i < q.workers; i++ {
		q.wg.Add(1)
		go q.workLoop(ready)
	}
	// Wait until all workers have entered their select loop. This eliminates
	// a startup race where Enqueue may run before any worker is parked on
	// the channel, causing a small buffer to overflow despite the worker
	// pool having idle capacity.
	for i := 0; i < q.workers; i++ {
		<-ready
	}
}

func (q *Queue) workLoop(ready chan<- struct{}) {
	defer q.wg.Done()
	ready <- struct{}{}
	for {
		select {
		case <-q.rootCtx.Done():
			return
		case j, ok := <-q.ch:
			if !ok {
				return
			}
			q.handler(q.rootCtx, j)
			if j.UserID != 0 {
				q.ReleaseUser(j.UserID)
			}
		}
	}
}

// Enqueue tries to insert a job; returns false if the queue is full.
func (q *Queue) Enqueue(j Job) bool {
	select {
	case q.ch <- j:
		return true
	default:
		return false
	}
}

// TryAcquireUser is a non-blocking attempt to claim the per-user slot.
func (q *Queue) TryAcquireUser(userID int64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, busy := q.locks[userID]; busy {
		return false
	}
	q.locks[userID] = struct{}{}
	return true
}

func (q *Queue) ReleaseUser(userID int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.locks, userID)
}

// Stop closes the channel and waits for in-flight jobs. If ctx fires first,
// it cancels rootCtx (jobs that respect it will return early) and still waits
// for workers to exit.
func (q *Queue) Stop(ctx context.Context) {
	q.stopOnce.Do(func() {
		close(q.ch)
		done := make(chan struct{})
		go func() { q.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			q.cancel()
			<-done
		}
	})
}
