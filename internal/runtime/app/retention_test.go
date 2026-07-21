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
	var logs bytes.Buffer
	application := &Application{Config: cfg, Repository: repo, Logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		application.runRetention(ctx)
		close(done)
	}()
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	time.Sleep(20 * time.Millisecond)
	past := time.Now().UTC().Add(-time.Hour)
	capsule := types.RunCapsule{SchemaVersion: types.SchemaVersion, CapsuleID: "afr_scheduled_retention", TraceID: "scheduled-retention", ProjectID: "short-lived", ServiceName: "agent", StartTime: past, EndTime: past, ExpiresAt: &past, TerminalStatus: "ok"}
	if err := repo.PutCapsule(ctx, capsule, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := repo.GetCapsule(ctx, capsule.CapsuleID); isNotFoundError(err) {
			stop()
			if !strings.Contains(logs.String(), `"msg":"retention completed"`) || !strings.Contains(logs.String(), `"capsule_count":1`) {
				t.Fatalf("scheduled retention log = %s", logs.String())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("scheduled retention did not remove %s; logs=%s", capsule.CapsuleID, logs.String())
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
