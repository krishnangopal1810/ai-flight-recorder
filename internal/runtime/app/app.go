package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/providers/oci"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/runtime/api"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/runtime/otlp"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/service"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type Application struct {
	Config     config.Config
	Repository repository.Repository
	Capsules   *service.CapsuleService
	Bundles    *service.BundleService
	Diffs      *service.DiffService
	Auth       *service.AuthService
	Replays    *service.ReplayService
	Evaluators *service.EvaluatorService
	Jobs       *service.JobService
	Receiver   *otlp.Receiver
	API        *api.Server
	Logger     *slog.Logger
}

func New(ctx context.Context, cfg config.Config) (*Application, error) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	var repo repository.Repository
	var err error
	switch cfg.Storage.Driver {
	case "sqlite":
		repo, err = repository.OpenLocal(ctx, cfg.Storage.SQLitePath, cfg.Storage.BlobPath)
	case "postgres":
		repo, err = repository.OpenHosted(ctx, cfg.Storage)
	default:
		return nil, types.NewError(types.ErrValidation, "unsupported storage driver", "use sqlite or postgres", nil)
	}
	if err != nil {
		return nil, err
	}
	policy, err := service.NewPolicyEngine(cfg.Policy)
	if err != nil {
		_ = repo.Close()
		return nil, err
	}
	capsules := service.NewCapsuleService(repo, policy)
	bundles := service.NewBundleService(repo)
	diffs := service.NewDiffService()
	auth := service.NewAuthService(repo)
	if err := auth.EnsureBootstrap(ctx, cfg.Auth.BootstrapToken); err != nil {
		_ = repo.Close()
		return nil, err
	}
	receiver := otlp.NewReceiver(capsules, cfg.Server.LateSpanGrace)
	replayReceiver := otlp.NewReceiver(capsules)
	ociRunner := oci.NewDockerRunner(cfg.Runtime.OCIBinary)
	replays := service.NewReplayService(repo, capsules, replayReceiver, ociRunner, diffs, cfg.Runtime.ReplayCredentials)
	evaluators := service.NewEvaluatorService(repo, capsules, diffs, ociRunner, cfg.Runtime.ReplayCredentials)
	jobs := service.NewJobService(repo, replays)
	apiServer := api.NewServer(cfg, capsules, bundles, diffs, auth, replays, evaluators, jobs, receiver, logger)
	return &Application{Config: cfg, Repository: repo, Capsules: capsules, Bundles: bundles, Diffs: diffs, Auth: auth, Replays: replays, Evaluators: evaluators, Jobs: jobs, Receiver: receiver, API: apiServer, Logger: logger}, nil
}

func (a *Application) Close() error { return a.Repository.Close() }

func (a *Application) Serve(ctx context.Context) error {
	grpcListener, err := net.Listen("tcp", a.Config.Server.OTLPGRPCAddress)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot bind OTLP gRPC listener", "choose an available loopback address or stop the conflicting process", err)
	}
	grpcOptions := []grpc.ServerOption{grpc.MaxRecvMsgSize(64 << 20)}
	if a.Config.Server.TLSCert != "" {
		transport, err := credentials.NewServerTLSFromFile(a.Config.Server.TLSCert, a.Config.Server.TLSKey)
		if err != nil {
			_ = grpcListener.Close()
			return types.NewError(types.ErrValidation, "cannot load OTLP gRPC TLS identity", "check tls_cert and tls_key PEM files", err)
		}
		grpcOptions = append(grpcOptions, grpc.Creds(transport))
	}
	grpcServer := grpc.NewServer(grpcOptions...)
	collectortrace.RegisterTraceServiceServer(grpcServer, a.Receiver)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errorsCh := make(chan error, 3)
	var once sync.Once
	stop := func() { once.Do(func() { grpcServer.GracefulStop(); cancel() }) }
	go func() {
		if err := grpcServer.Serve(grpcListener); err != nil {
			errorsCh <- err
		}
	}()
	go a.Jobs.Run(ctx)
	go a.runRetention(ctx)
	go func() {
		errorsCh <- api.ListenAndServe(ctx, a.Config.Server.HTTPAddress, a.Config.Server.TLSCert, a.Config.Server.TLSKey, a.API.Handler())
	}()
	if a.Config.Server.OTLPHTTPAddress != a.Config.Server.HTTPAddress {
		go func() {
			errorsCh <- api.ListenAndServe(ctx, a.Config.Server.OTLPHTTPAddress, a.Config.Server.TLSCert, a.Config.Server.TLSKey, a.API.Handler())
		}()
	}
	a.Logger.Info("flight recorder started", "http_address", a.Config.Server.HTTPAddress, "otlp_http_address", a.Config.Server.OTLPHTTPAddress, "otlp_grpc_address", a.Config.Server.OTLPGRPCAddress, "storage", fmt.Sprint(a.Repository))
	select {
	case <-ctx.Done():
		stop()
		return nil
	case serveErr := <-errorsCh:
		stop()
		if errors.Is(serveErr, context.Canceled) {
			return nil
		}
		return serveErr
	}
}

func (a *Application) runRetention(ctx context.Context) {
	run := func() {
		deleted, err := a.Repository.DeleteExpired(ctx, time.Now().UTC())
		if err != nil {
			a.Logger.Error("retention evaluation failed", "code", errorCode(err))
			return
		}
		if deleted > 0 {
			a.Logger.Info("retention completed", "capsule_count", deleted)
		}
	}
	run()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func errorCode(err error) string {
	if typed, ok := err.(*types.FlightError); ok {
		return string(typed.Code)
	}
	return "internal"
}
