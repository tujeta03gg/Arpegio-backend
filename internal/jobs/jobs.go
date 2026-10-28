// Package jobs runs background work stored in SQLite: retries with backoff, and it survives restarts.
// There is no polling: the worker sleeps until a job is enqueued or the next retry is due.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"arpegio/internal/events"
	"arpegio/internal/store"
	"arpegio/internal/store/db"
)

// timeFmt is fixed-width so run_after compares correctly as text.
const timeFmt = "2006-01-02T15:04:05.000000000Z"

// Handler does the work of one job. Returning an error schedules a retry.
type Handler func(ctx context.Context, payload json.RawMessage) error

type Service struct {
	st          *store.Store
	bus         *events.Bus
	log         *slog.Logger
	handlers    map[string]Handler
	wake        chan struct{}
	now         func() time.Time
	maxAttempts int
	backoff     func(attempt int) time.Duration
	mu          sync.Mutex // guards handlers
}

func New(st *store.Store, bus *events.Bus, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		st: st, bus: bus, log: log, handlers: map[string]Handler{}, wake: make(chan struct{}, 1),
		now: time.Now, maxAttempts: 5,
		backoff: func(attempt int) time.Duration { return time.Duration(1<<attempt) * 5 * time.Second }, // 10s, 20s, 40s...
	}
}

// Handle registers the handler for a job kind.
func (s *Service) Handle(kind string, h Handler) {
	s.mu.Lock()
	s.handlers[kind] = h
	s.mu.Unlock()
}

// Enqueue stores a job and wakes the worker. payload is stored as JSON.
func (s *Service) Enqueue(ctx context.Context, kind string, payload any) error {
	blob, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("jobs: encode payload for %s: %w", kind, err)
	}
	err = db.New(s.st.W).InsertJob(ctx, db.InsertJobParams{
		ID: store.NewID(), Kind: kind, Payload: string(blob), RunAfter: s.now().UTC().Format(timeFmt),
	})
	if err != nil {
		return fmt.Errorf("jobs: enqueue %s: %w", kind, err)
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

// Open counts jobs that are waiting or running.
func (s *Service) Open(ctx context.Context) (int, error) {
	n, err := db.New(s.st.R).CountOpenJobs(ctx)
	if err != nil {
		return 0, fmt.Errorf("jobs: count: %w", err)
	}
	return int(n), nil
}

// Failed counts jobs that ran out of attempts.
func (s *Service) Failed(ctx context.Context) (int, error) {
	n, err := db.New(s.st.R).CountFailedJobs(ctx)
	if err != nil {
		return 0, fmt.Errorf("jobs: count failed: %w", err)
	}
	return int(n), nil
}

// Run processes jobs until ctx ends. Jobs left "running" by a crash are retried first.
func (s *Service) Run(ctx context.Context) {
	if err := db.New(s.st.W).ResetRunningJobs(ctx); err != nil {
		s.log.Error("jobs: reset running jobs", slog.Any("err", err))
	}
	for ctx.Err() == nil {
		s.drain(ctx)
		timer := s.timerForNext(ctx)
		select {
		case <-ctx.Done():
		case <-s.wake:
		case <-timer:
		}
	}
}

// timerForNext fires when the earliest waiting job is due; nil (blocks forever) when nothing is waiting.
func (s *Service) timerForNext(ctx context.Context) <-chan time.Time {
	next, err := db.New(s.st.R).NextJobTime(ctx)
	if err != nil || next == "" {
		return nil
	}
	due, err := time.Parse(timeFmt, next)
	if err != nil {
		return nil
	}
	return time.After(max(time.Until(due), time.Millisecond))
}

// drain runs every job that is due, one at a time.
func (s *Service) drain(ctx context.Context) {
	for ctx.Err() == nil {
		job, err := db.New(s.st.R).NextReadyJob(ctx, s.now().UTC().Format(timeFmt))
		if errors.Is(err, sql.ErrNoRows) {
			return
		}
		if err != nil {
			s.log.Error("jobs: look for work", slog.Any("err", err))
			return
		}
		s.run(ctx, job)
	}
}

func (s *Service) run(ctx context.Context, job db.Job) {
	q := db.New(s.st.W)
	if err := q.StartJob(ctx, job.ID); err != nil {
		s.log.Error("jobs: start", slog.String("job_id", job.ID), slog.Any("err", err))
		return
	}
	attempts := int(job.Attempts) + 1

	s.mu.Lock()
	h := s.handlers[job.Kind]
	s.mu.Unlock()
	var herr error
	if h == nil {
		herr = fmt.Errorf("no handler for job kind %q", job.Kind)
	} else {
		herr = safely(ctx, h, json.RawMessage(job.Payload))
	}

	status, runAfter, lastErr := "done", job.RunAfter, sql.NullString{}
	switch {
	case herr == nil:
	case attempts >= s.maxAttempts || h == nil:
		status, lastErr = "failed", sql.NullString{String: herr.Error(), Valid: true}
		s.log.Error("jobs: giving up", slog.String("job_id", job.ID), slog.String("kind", job.Kind), slog.Any("err", herr))
	default:
		status, lastErr = "pending", sql.NullString{String: herr.Error(), Valid: true}
		runAfter = s.now().Add(s.backoff(attempts)).UTC().Format(timeFmt)
		s.log.Warn("jobs: will retry", slog.String("job_id", job.ID), slog.String("kind", job.Kind), slog.Int("attempt", attempts), slog.Any("err", herr))
	}
	if err := q.FinishJob(ctx, db.FinishJobParams{Status: status, RunAfter: runAfter, LastError: lastErr, ID: job.ID}); err != nil {
		s.log.Error("jobs: finish", slog.String("job_id", job.ID), slog.Any("err", err))
		return
	}
	if status != "pending" {
		s.bus.Publish(events.Event{Type: "job.finished", Data: map[string]string{"id": job.ID, "kind": job.Kind, "status": status}})
	}
	if status == "done" {
		_ = q.PruneDoneJobs(ctx) // keep the table small on the SD card
	}
}

// safely turns a handler panic into an error so one bad job cannot kill the worker.
func safely(ctx context.Context, h Handler, payload json.RawMessage) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panicked: %v", r)
		}
	}()
	return h(ctx, payload)
}
