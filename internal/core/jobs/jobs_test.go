package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func statusOf(s *Scheduler, name string) Status {
	for _, st := range s.Status() {
		if st.Name == name {
			return st
		}
	}
	return Status{}
}

func TestEveryRunsOnInterval(t *testing.T) {
	s := New(quiet())
	var n atomic.Int64
	if err := s.Every("tick", 10*time.Millisecond, func(context.Context) error { n.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Stop()
	waitFor(t, "three runs", func() bool { return n.Load() >= 3 })

	st := statusOf(s, "tick")
	if st.Runs < 3 || st.LastRun.IsZero() || st.LastErr != "" {
		t.Errorf("status = %+v", st)
	}
}

func TestRunNowRecordsErrorAndDuration(t *testing.T) {
	s := New(quiet())
	boom := errors.New("boom")
	_ = s.Every("slow", time.Hour, func(context.Context) error { time.Sleep(20 * time.Millisecond); return boom })

	if err := s.RunNow("slow"); !errors.Is(err, boom) {
		t.Fatalf("RunNow = %v, want boom", err)
	}
	st := statusOf(s, "slow")
	if st.Runs != 1 || st.Failures != 1 || st.LastErr != "boom" || st.Duration < 20*time.Millisecond || st.LastRun.IsZero() {
		t.Errorf("status = %+v", st)
	}
	if err := s.RunNow("missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RunNow(missing) = %v, want ErrNotFound", err)
	}
}

func TestPanicIsRecoveredAndJobKeepsRunning(t *testing.T) {
	s := New(quiet())
	var calls atomic.Int64
	_ = s.Every("flaky", 10*time.Millisecond, func(context.Context) error {
		if calls.Add(1) == 1 {
			panic("kaboom")
		}
		return nil
	})
	s.Start()
	defer s.Stop()

	// First run panics; the loop must survive and run again cleanly.
	waitFor(t, "a run after the panic", func() bool { return calls.Load() >= 3 })
	st := statusOf(s, "flaky")
	if st.Failures != 1 {
		t.Errorf("failures = %d, want 1", st.Failures)
	}
	if st.LastErr != "" {
		t.Errorf("a later successful run should clear the error, got %q", st.LastErr)
	}
}

func TestRunNowReportsPanic(t *testing.T) {
	s := New(quiet())
	_ = s.Every("p", time.Hour, func(context.Context) error { panic("oh no") })
	err := s.RunNow("p")
	var pe *PanicError
	if !errors.As(err, &pe) || pe.Value != "oh no" || len(pe.Stack) == 0 {
		t.Fatalf("RunNow = %v, want *PanicError with stack", err)
	}
	if got := statusOf(s, "p").LastErr; got != "panic: oh no" {
		t.Errorf("LastErr = %q", got)
	}
	// And the scheduler still works afterwards.
	_ = s.Every("ok", time.Hour, func(context.Context) error { return nil })
	if err := s.RunNow("ok"); err != nil {
		t.Errorf("RunNow(ok) = %v", err)
	}
}

func TestJobsDoNotOverlap(t *testing.T) {
	s := New(quiet())
	started, release := make(chan struct{}), make(chan struct{})
	_ = s.Every("one", time.Hour, func(context.Context) error { close(started); <-release; return nil })

	done := make(chan error, 1)
	go func() { done <- s.RunNow("one") }()
	<-started
	if err := s.RunNow("one"); !errors.Is(err, ErrRunning) {
		t.Errorf("second RunNow = %v, want ErrRunning", err)
	}
	if !statusOf(s, "one").Running {
		t.Error("status should show Running")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestStopCancelsContextAndWaits(t *testing.T) {
	s := New(quiet())
	entered, exited := make(chan struct{}), make(chan struct{})
	_ = s.Every("blocker", 5*time.Millisecond, func(ctx context.Context) error {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-ctx.Done()
		select {
		case <-exited:
		default:
			close(exited)
		}
		return ctx.Err()
	})
	s.Start()
	<-entered
	s.Stop() // must return only after the job saw the cancellation
	select {
	case <-exited:
	default:
		t.Fatal("Stop returned before the job observed cancellation")
	}
}

func TestEveryValidates(t *testing.T) {
	s := New(quiet())
	ok := func(context.Context) error { return nil }
	if s.Every("", time.Second, ok) == nil {
		t.Error("empty name accepted")
	}
	if s.Every("x", 0, ok) == nil {
		t.Error("zero interval accepted")
	}
	if s.Every("x", time.Second, nil) == nil {
		t.Error("nil func accepted")
	}
	if err := s.Every("x", time.Second, ok); err != nil {
		t.Fatal(err)
	}
	if s.Every("x", time.Second, ok) == nil {
		t.Error("duplicate name accepted")
	}
}

func TestEveryAfterStartLaunchesImmediately(t *testing.T) {
	s := New(quiet())
	s.Start()
	defer s.Stop()
	var n atomic.Int64
	_ = s.Every("late", 10*time.Millisecond, func(context.Context) error { n.Add(1); return nil })
	waitFor(t, "late job to run", func() bool { return n.Load() >= 1 })
}
