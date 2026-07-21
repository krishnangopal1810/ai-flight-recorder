package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestAFRSEC004RetentionSchedulerRemovesExpiredCapsules(t *testing.T) {
	directory := t.TempDir()
	cfg := config.Default(directory)
	cfg.Server.RetentionInterval = 10 * time.Millisecond
	repo, err := repository.OpenLocal(context.Background(), filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	observed := &observedRetentionRepository{Repository: repo, completed: make(chan retentionOutcome, 8)}
	var logs synchronizedBuffer
	application := &Application{Config: cfg, Repository: observed, Logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		application.runRetention(ctx)
		close(done)
	}()
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	initial := waitForRetentionOutcome(t, observed.completed)
	if initial.deleted != 0 || initial.err != nil {
		t.Fatalf("initial retention pass = %d, %v", initial.deleted, initial.err)
	}
	past := time.Now().UTC().Add(-time.Hour)
	capsule := types.RunCapsule{SchemaVersion: types.SchemaVersion, CapsuleID: "afr_scheduled_retention", TraceID: "scheduled-retention", ProjectID: "short-lived", ServiceName: "agent", StartTime: past, EndTime: past, ExpiresAt: &past, TerminalStatus: "ok"}
	if err := repo.PutCapsule(ctx, capsule, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case outcome := <-observed.completed:
			if outcome.err != nil {
				t.Fatalf("scheduled retention pass = %d, %v", outcome.deleted, outcome.err)
			}
			if outcome.deleted == 0 {
				continue
			}
			if outcome.deleted != 1 {
				t.Fatalf("scheduled retention deleted %d capsules", outcome.deleted)
			}
			waitForRetentionLog(t, &logs)
			if _, err := repo.GetCapsule(ctx, capsule.CapsuleID); !isNotFoundError(err) {
				t.Fatalf("expired capsule remains after completed retention pass: %v", err)
			}
			stop()
			return
		case <-deadline.C:
			t.Fatalf("scheduled retention did not remove %s; logs=%s", capsule.CapsuleID, logs.String())
		}
	}
}

type retentionOutcome struct {
	deleted int
	err     error
}

type observedRetentionRepository struct {
	repository.Repository
	completed chan retentionOutcome
}

func (r *observedRetentionRepository) DeleteExpired(ctx context.Context, now time.Time) (int, error) {
	deleted, err := r.Repository.DeleteExpired(ctx, now)
	r.completed <- retentionOutcome{deleted: deleted, err: err}
	return deleted, err
}

func waitForRetentionOutcome(t *testing.T, completed <-chan retentionOutcome) retentionOutcome {
	t.Helper()
	select {
	case outcome := <-completed:
		return outcome
	case <-time.After(time.Second):
		t.Fatal("retention pass did not complete")
		return retentionOutcome{}
	}
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func waitForRetentionLog(t *testing.T, logs *synchronizedBuffer) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		output := logs.String()
		if strings.Contains(output, `"msg":"retention completed"`) && strings.Contains(output, `"capsule_count":1`) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("scheduled retention completion was not logged: %s", logs.String())
}

type failingRetentionRepository struct {
	repository.Repository
	err error
}

func (r failingRetentionRepository) DeleteExpired(context.Context, time.Time) (int, error) {
	return 2, r.err
}

func TestAFRSEC004RetentionFailureEmitsPayloadFreeOperationalAlert(t *testing.T) {
	injected := types.NewError(types.ErrStorage, "unsafe accounting seed must not be logged", "retain data", errors.New("private backend cause"))
	var logs bytes.Buffer
	cfg := config.Default(t.TempDir())
	application := &Application{
		Config:     cfg,
		Repository: failingRetentionRepository{err: injected},
		Logger:     slog.New(slog.NewJSONHandler(&logs, nil)),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	application.runRetention(ctx)
	output := logs.String()
	if !strings.Contains(output, `"msg":"retention evaluation failed"`) || !strings.Contains(output, `"code":"storage"`) || !strings.Contains(output, `"capsule_count":2`) {
		t.Fatalf("retention operational alert = %s", output)
	}
	for _, forbidden := range []string{"unsafe accounting seed", "private backend cause"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("retention alert leaked %q: %s", forbidden, output)
		}
	}
}

func isNotFoundError(err error) bool {
	var flightErr *types.FlightError
	return errors.As(err, &flightErr) && flightErr.Code == types.ErrNotFound
}
