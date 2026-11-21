// Package jobs is a small in-process scheduler for recurring background work
// (polling download clients, refreshing metadata, cleaning old sessions).
//
// Each job runs on its own goroutine, never overlaps itself, survives panics,
// and remembers when it last ran, how long it took, and how it ended.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"sync"
	"time"
)

// Errors returned by the scheduler.
var (
	ErrNotFound = errors.New("jobs: no such job")
	ErrRunning  = errors.New("jobs: job is already running")
)

// Func is the work of one job. It must return when ctx is cancelled.
type Func func(ctx context.Context) error

// PanicError is the error recorded when a job panics.
type PanicError struct {
	Value any
	Stack []byte
}

func (p *PanicError) Error() string { return fmt.Sprintf("panic: %v", p.Value) }

// Status is a snapshot of one job.
type Status struct {
	Name     string        `json:"name"`
	Interval time.Duration `json:"interval"`
	Running  bool          `json:"running"`
	Runs     int64         `json:"runs"`
	Failures int64         `json:"failures"`
	LastRun  time.Time     `json:"lastRun"`
	Duration time.Duration `json:"duration"`
	LastErr  string        `json:"lastError,omitempty"`
}

type job struct {
	name     string
	interval time.Duration
	fn       Func

	mu       sync.Mutex
	running  bool
	runs     int64
	failures int64
	lastRun  time.Time
	duration time.Duration
	lastErr  string
}

// Scheduler owns a set of jobs.
type Scheduler struct {
	log    *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	jobs    map[string]*job
	started bool
}

// New creates a scheduler. Jobs do not run until Start.
func New(log *slog.Logger) *Scheduler {
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{log: log, ctx: ctx, cancel: cancel, jobs: map[string]*job{}}
}

// Every registers fn to run each interval (the first run is one interval after
// Start, or after registration if the scheduler is already running). Use
// RunNow to run it immediately.
func (s *Scheduler) Every(name string, interval time.Duration, fn Func) error {
	if name == "" || fn == nil {
		return errors.New("jobs: a job needs a name and a function")
	}
	if interval <= 0 {
		return fmt.Errorf("jobs: job %q needs a positive interval", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.jobs[name]; dup {
		return fmt.Errorf("jobs: job %q is already registered", name)
	}
	j := &job{name: name, interval: interval, fn: fn}
	s.jobs[name] = j
	if s.started {
		s.launch(j)
	}
	return nil
}

// Start begins running every registered job on its interval.
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return
	}
	s.started = true
	for _, j := range s.jobs {
		s.launch(j)
	}
}

func (s *Scheduler) launch(j *job) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(j.interval)
		defer t.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-t.C:
				// Skip the tick if a RunNow is still in flight.
				if err := s.run(j); err != nil && !errors.Is(err, ErrRunning) {
					s.log.Warn("job failed", "job", j.name, "err", err)
				}
			}
		}
	}()
}

// RunNow runs a job immediately on the caller's goroutine and returns its
// error (a *PanicError if it panicked). It returns ErrRunning if the job is
// already in progress and ErrNotFound for an unknown name.
func (s *Scheduler) RunNow(name string) error {
	s.mu.Lock()
	j := s.jobs[name]
	s.mu.Unlock()
	if j == nil {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return s.run(j)
}

func (s *Scheduler) run(j *job) (err error) {
	j.mu.Lock()
	if j.running {
		j.mu.Unlock()
		return ErrRunning
	}
	j.running = true
	j.mu.Unlock()

	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			pe := &PanicError{Value: r, Stack: debug.Stack()}
			s.log.Error("job panicked", "job", j.name, "panic", fmt.Sprint(r), "stack", string(pe.Stack))
			err = pe
		}
		j.mu.Lock()
		j.running = false
		j.runs++
		j.lastRun = start
		j.duration = time.Since(start)
		if err != nil {
			j.failures++
			j.lastErr = err.Error()
		} else {
			j.lastErr = ""
		}
		j.mu.Unlock()
	}()
	return j.fn(s.ctx)
}

// Status returns a snapshot of every job, sorted by name.
func (s *Scheduler) Status() []Status {
	s.mu.Lock()
	list := make([]*job, 0, len(s.jobs))
	for _, j := range s.jobs {
		list = append(list, j)
	}
	s.mu.Unlock()
	out := make([]Status, 0, len(list))
	for _, j := range list {
		j.mu.Lock()
		out = append(out, Status{
			Name: j.name, Interval: j.interval, Running: j.running,
			Runs: j.runs, Failures: j.failures,
			LastRun: j.lastRun, Duration: j.duration, LastErr: j.lastErr,
		})
		j.mu.Unlock()
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// Stop cancels every job's context and waits for the loops to finish.
func (s *Scheduler) Stop() {
	s.cancel()
	s.wg.Wait()
}
