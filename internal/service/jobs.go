package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type JobService struct {
	repository repository.Repository
	replay     *ReplayService
	owner      string
}

func NewJobService(repo repository.Repository, replay *ReplayService) *JobService {
	return &JobService{repository: repo, replay: replay, owner: "worker-" + uuid.NewString()}
}
func (s *JobService) Get(ctx context.Context, id string) (types.Job, error) {
	return s.repository.GetJob(ctx, id)
}
func (s *JobService) Events(ctx context.Context, id string, after int64) ([]types.JobEvent, error) {
	return s.repository.ListJobEvents(ctx, id, after)
}

func (s *JobService) Run(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			job, err := s.repository.ClaimJob(ctx, s.owner, time.Now().UTC().Add(30*time.Second))
			if err != nil || job == nil {
				continue
			}
			executionCtx, cancelExecution := context.WithCancel(ctx)
			go s.renewLease(executionCtx, job.ID, cancelExecution)
			switch job.Kind {
			case "replay":
				_ = s.replay.RunClaimed(executionCtx, *job)
			default:
				job.State, job.UpdatedAt = types.JobFailed, time.Now().UTC()
				job.Error = types.NewError(types.ErrValidation, "unknown job kind", "upgrade the worker or recreate the job", nil)
				_ = s.repository.UpdateJob(executionCtx, *job)
			}
			cancelExecution()
		}
	}
}

func (s *JobService) renewLease(ctx context.Context, jobID string, cancelExecution context.CancelFunc) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.repository.RenewJobLease(ctx, jobID, s.owner, time.Now().UTC().Add(30*time.Second)) != nil {
				cancelExecution()
				return
			}
		}
	}
}
