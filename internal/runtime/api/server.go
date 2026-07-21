package api

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/providers/otlpjson"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/runtime/otlp"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/service"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type Server struct {
	config     config.Config
	capsules   *service.CapsuleService
	bundles    *service.BundleService
	diffs      *service.DiffService
	auth       *service.AuthService
	replays    *service.ReplayService
	evaluators *service.EvaluatorService
	jobs       *service.JobService
	receiver   *otlp.Receiver
	logger     *slog.Logger
	accepted   atomic.Int64
	rejected   atomic.Int64
}

func NewServer(cfg config.Config, capsules *service.CapsuleService, bundles *service.BundleService, diffs *service.DiffService, auth *service.AuthService, replays *service.ReplayService, evaluators *service.EvaluatorService, jobs *service.JobService, receiver *otlp.Receiver, logger *slog.Logger) *Server {
	return &Server{config: cfg, capsules: capsules, bundles: bundles, diffs: diffs, auth: auth, replays: replays, evaluators: evaluators, jobs: jobs, receiver: receiver, logger: logger}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("GET /api/v1/capsules", s.withScope("read", s.listCapsules))
	mux.HandleFunc("GET /api/v1/capsules/{id}", s.withScope("read", s.getCapsule))
	mux.HandleFunc("GET /api/v1/capsules/{id}/completeness", s.withScope("read", s.getCompleteness))
	mux.HandleFunc("GET /api/v1/capsules/{id}/playback", s.withScope("read", s.playback))
	mux.HandleFunc("GET /api/v1/capsules/{id}/payloads/{digest}", s.withScope("read", s.getPayload))
	mux.HandleFunc("GET /api/v1/capsules/{id}/export", s.withScope("read", s.exportCapsule))
	mux.HandleFunc("POST /api/v1/capsules/import", s.withScope("ingest", s.importCapsule))
	mux.HandleFunc("POST /api/v1/diffs", s.withScope("read", s.diffCapsules))
	mux.HandleFunc("POST /api/v1/replays", s.withScope("replay", s.createReplay))
	mux.HandleFunc("POST /api/v1/bisects", s.withScope("replay", s.bisect))
	mux.HandleFunc("POST /api/v1/evaluators/validate", s.withScope("replay", s.validateEvaluator))
	mux.HandleFunc("POST /api/v1/evaluators/run", s.withScope("replay", s.runEvaluator))
	mux.HandleFunc("GET /api/v1/evaluators/{id}", s.withScope("read", s.getEvaluator))
	mux.HandleFunc("GET /api/v1/capsules/{id}/evaluations", s.withScope("read", s.evaluatorRuns))
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.withScope("read", s.getJob))
	mux.HandleFunc("GET /api/v1/jobs/{id}/events", s.withScope("read", s.jobEvents))
	mux.HandleFunc("POST /api/v1/tokens", s.withScope("admin", s.createToken))
	mux.HandleFunc("DELETE /api/v1/tokens/{id}", s.withScope("admin", s.revokeToken))
	mux.HandleFunc("GET /api/v1/audit-events", s.withScope("admin", s.auditEvents))
	mux.HandleFunc("POST /v1/traces", s.withScope("ingest", s.otlpHTTP))
	if info, err := os.Stat(s.config.Server.WebDir); err == nil && info.IsDir() {
		mux.Handle("/", inspectorHandler(s.config.Server.WebDir))
	}
	return s.recoverMiddleware(s.loggingMiddleware(mux))
}

func inspectorHandler(directory string) http.Handler {
	files := http.FileServer(http.Dir(directory))
	index := filepath.Join(directory, "index.html")
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api" || request.URL.Path == "/v1" || strings.HasPrefix(request.URL.Path, "/api/") || strings.HasPrefix(request.URL.Path, "/v1/") {
			http.NotFound(writer, request)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			http.NotFound(writer, request)
			return
		}
		relative := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(request.URL.Path, "/")))
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			http.NotFound(writer, request)
			return
		}
		candidate := filepath.Join(directory, relative)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			files.ServeHTTP(writer, request)
			return
		}
		if relative != "." && filepath.Ext(relative) != "" {
			http.NotFound(writer, request)
			return
		}
		http.ServeFile(writer, request, index)
	})
}

func (s *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok", "version": types.SchemaVersion})
}
func (s *Server) metrics(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(writer, "flight_otlp_accepted_spans_total %d\nflight_otlp_rejected_spans_total %d\n", s.accepted.Load(), s.rejected.Load())
}

func (s *Server) listCapsules(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	items, err := s.capsules.List(request.Context(), request.URL.Query().Get("project"), limit)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getCapsule(writer http.ResponseWriter, request *http.Request) {
	capsule, err := s.capsules.Get(request.Context(), request.PathValue("id"), true)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, capsule)
}

func (s *Server) getCompleteness(writer http.ResponseWriter, request *http.Request) {
	report, err := s.capsules.Completeness(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, report)
}

func (s *Server) playback(writer http.ResponseWriter, request *http.Request) {
	capsule, err := s.capsules.Get(request.Context(), request.PathValue("id"), false)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"capsule": capsule, "mode": "playback", "executed": false})
}

func (s *Server) getPayload(writer http.ResponseWriter, request *http.Request) {
	digest := request.PathValue("digest")
	if !strings.HasPrefix(digest, "sha256:") {
		digest = "sha256:" + digest
	}
	data, err := s.capsules.Payload(request.Context(), digest)
	if err != nil {
		writeError(writer, err)
		return
	}
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.Header().Set("Cache-Control", "private, immutable")
	_, _ = writer.Write(data)
}

func (s *Server) exportCapsule(writer http.ResponseWriter, request *http.Request) {
	capsule, err := s.capsules.Get(request.Context(), request.PathValue("id"), true)
	if err != nil {
		writeError(writer, err)
		return
	}
	writer.Header().Set("Content-Type", "application/zstd")
	writer.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", capsule.CapsuleID+".tar.zst"))
	if err := s.bundles.Export(request.Context(), capsule, writer); err != nil {
		s.logger.Error("bundle export failed", "code", errorCode(err))
		return
	}
}

func (s *Server) importCapsule(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, 2<<30)
	capsule, err := s.bundles.Import(request.Context(), request.Body)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, capsule)
}

func (s *Server) diffCapsules(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Baseline  string `json:"baseline_capsule_id"`
		Candidate string `json:"candidate_capsule_id"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, err)
		return
	}
	baseline, err := s.capsules.Get(request.Context(), body.Baseline, true)
	if err != nil {
		writeError(writer, err)
		return
	}
	candidate, err := s.capsules.Get(request.Context(), body.Candidate, true)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, s.diffs.Compare(baseline, candidate))
}

func (s *Server) createToken(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, err)
		return
	}
	token, err := s.auth.Create(request.Context(), body.Name, body.Scopes)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, token)
}

func (s *Server) revokeToken(writer http.ResponseWriter, request *http.Request) {
	if err := s.auth.Revoke(request.Context(), request.PathValue("id")); err != nil {
		writeError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) auditEvents(writer http.ResponseWriter, request *http.Request) {
	after, _ := strconv.ParseInt(request.URL.Query().Get("after"), 10, 64)
	events, err := s.auth.Audit(request.Context(), after)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": events})
}

func (s *Server) createReplay(writer http.ResponseWriter, request *http.Request) {
	var body types.CreateReplayRequest
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, err)
		return
	}
	job, err := s.replays.Create(request.Context(), body)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, job)
}

func (s *Server) bisect(writer http.ResponseWriter, request *http.Request) {
	var body types.BisectRequest
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, err)
		return
	}
	byRevision := make(map[string]types.RevisionEvaluation, len(body.Evaluations))
	for _, evaluation := range body.Evaluations {
		if evaluation.Revision == "" || byRevision[evaluation.Revision].Revision != "" {
			writeError(writer, types.NewError(types.ErrValidation, "bisect evaluations are invalid", "provide exactly one evaluation for each ordered revision", nil))
			return
		}
		byRevision[evaluation.Revision] = evaluation
	}
	result, err := service.NewBisectService().Search(request.Context(), body.Revisions, body.AssumeMonotonic, func(_ context.Context, revision string) (types.RevisionEvaluation, error) {
		evaluation, ok := byRevision[revision]
		if !ok {
			return types.RevisionEvaluation{}, types.NewError(types.ErrInconclusive, "revision evaluation is missing", "run the controlled replay and evaluator for "+revision, nil)
		}
		return evaluation, nil
	})
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) validateEvaluator(writer http.ResponseWriter, request *http.Request) {
	var definition types.EvaluatorDefinition
	if err := decodeJSON(request, &definition); err != nil {
		writeError(writer, err)
		return
	}
	if err := s.evaluators.Validate(definition); err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"valid": true, "definition": definition})
}

func (s *Server) runEvaluator(writer http.ResponseWriter, request *http.Request) {
	var body types.EvaluatorRunRequest
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, err)
		return
	}
	run, err := s.evaluators.Run(request.Context(), body, s.config.Runtime.GatewayImage)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, run)
}

func (s *Server) evaluatorRuns(writer http.ResponseWriter, request *http.Request) {
	runs, err := s.evaluators.Runs(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": runs})
}

func (s *Server) getEvaluator(writer http.ResponseWriter, request *http.Request) {
	definition, err := s.evaluators.Definition(request.Context(), request.PathValue("id"), request.URL.Query().Get("version"))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, definition)
}

func (s *Server) getJob(writer http.ResponseWriter, request *http.Request) {
	job, err := s.jobs.Get(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, job)
}

func (s *Server) jobEvents(writer http.ResponseWriter, request *http.Request) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, types.NewError(types.ErrStorage, "event streaming is unavailable", "use GET /api/v1/jobs/{id} polling", nil))
		return
	}
	after, _ := strconv.ParseInt(request.Header.Get("Last-Event-ID"), 10, 64)
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		events, err := s.jobs.Events(request.Context(), request.PathValue("id"), after)
		if err != nil {
			return
		}
		for _, event := range events {
			_, _ = fmt.Fprintf(writer, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, event.Data)
			after = event.Sequence
			flusher.Flush()
		}
		job, err := s.jobs.Get(request.Context(), request.PathValue("id"))
		if err != nil {
			return
		}
		if job.State == types.JobCompleted || job.State == types.JobFailed || job.State == types.JobBlocked || job.State == types.JobInconclusive {
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) otlpHTTP(writer http.ResponseWriter, request *http.Request) {
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	jsonEncoding := mediaType == "application/json"
	if mediaType != "application/x-protobuf" && mediaType != "application/protobuf" && !jsonEncoding {
		writeError(writer, types.NewError(types.ErrValidation, "OTLP HTTP content type is unsupported", "send application/x-protobuf or application/json", nil))
		return
	}
	var body io.Reader = http.MaxBytesReader(writer, request.Body, 64<<20)
	if strings.EqualFold(request.Header.Get("Content-Encoding"), "gzip") {
		compressed, err := gzip.NewReader(body)
		if err != nil {
			writeError(writer, types.NewError(types.ErrValidation, "invalid OTLP gzip body", "send a valid gzip stream", err))
			return
		}
		defer compressed.Close()
		body = io.LimitReader(compressed, 64<<20)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		writeError(writer, types.NewError(types.ErrValidation, "cannot read OTLP request", "reduce the batch size", err))
		return
	}
	var exportRequest collectortrace.ExportTraceServiceRequest
	if jsonEncoding {
		data, err = otlpjson.NormalizeIDs(data)
		if err == nil {
			err = protojson.Unmarshal(data, &exportRequest)
		}
	} else {
		err = proto.Unmarshal(data, &exportRequest)
	}
	if err != nil {
		writeError(writer, types.NewError(types.ErrValidation, "malformed OTLP request", "send a valid ExportTraceServiceRequest", err))
		return
	}
	_, rejected, captureErr := s.receiver.Capture(request.Context(), &exportRequest)
	total := countSpans(&exportRequest)
	if captureErr != nil && rejected == 0 {
		rejected = total
	}
	if rejected > total {
		rejected = total
	}
	s.accepted.Add(int64(total - rejected))
	s.rejected.Add(int64(rejected))
	response := &collectortrace.ExportTraceServiceResponse{}
	if rejected > 0 || captureErr != nil {
		response.PartialSuccess = &collectortrace.ExportTracePartialSuccess{RejectedSpans: int64(rejected), ErrorMessage: safeMessage(captureErr)}
	}
	var encoded []byte
	var marshalErr error
	if jsonEncoding {
		encoded, marshalErr = protojson.Marshal(response)
	} else {
		encoded, marshalErr = proto.Marshal(response)
	}
	if marshalErr != nil {
		writeError(writer, marshalErr)
		return
	}
	if jsonEncoding {
		writer.Header().Set("Content-Type", "application/json")
	} else {
		writer.Header().Set("Content-Type", "application/x-protobuf")
	}
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(encoded)
}

func (s *Server) withScope(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if s.config.Auth.LocalBypass {
			next(writer, request)
			return
		}
		value := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
		if err := s.auth.Authorize(request.Context(), value, scope); err != nil {
			writeError(writer, err)
			return
		}
		next(writer, request)
	}
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		start := time.Now()
		next.ServeHTTP(writer, request)
		s.logger.Info("http request", "method", request.Method, "route", request.URL.Path, "duration_ms", time.Since(start).Milliseconds())
	})
}

func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("http panic", "type", fmt.Sprintf("%T", recovered))
				writeError(writer, types.NewError(types.ErrStorage, "internal service failure", "inspect local structured logs using the request timestamp", nil))
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

func decodeJSON(request *http.Request, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return types.NewError(types.ErrValidation, "invalid JSON request", "fix the request fields and JSON types", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return types.NewError(types.ErrValidation, "request contains multiple JSON values", "send exactly one JSON document", nil)
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
func writeError(writer http.ResponseWriter, err error) {
	typed, ok := err.(*types.FlightError)
	if !ok {
		typed = types.NewError(types.ErrStorage, "internal service failure", "inspect local structured logs", nil)
	}
	status := http.StatusInternalServerError
	switch typed.Code {
	case types.ErrValidation:
		status = http.StatusBadRequest
	case types.ErrAuthentication:
		status = http.StatusUnauthorized
	case types.ErrNotFound:
		status = http.StatusNotFound
	case types.ErrConflict:
		status = http.StatusConflict
	case types.ErrUnsafeReplay:
		status = http.StatusForbidden
	case types.ErrIncompleteCapsule, types.ErrUnmatchedBoundary, types.ErrInconclusive, types.ErrEvaluator:
		status = http.StatusUnprocessableEntity
	}
	writeJSON(writer, status, map[string]any{"error": typed})
}
func errorCode(err error) string {
	if typed, ok := err.(*types.FlightError); ok {
		return string(typed.Code)
	}
	return "internal"
}
func safeMessage(err error) string {
	if err == nil {
		return ""
	}
	if typed, ok := err.(*types.FlightError); ok {
		return string(typed.Code) + ": " + typed.Message
	}
	return "internal: request rejected"
}
func countSpans(request *collectortrace.ExportTraceServiceRequest) int {
	count := 0
	for _, resource := range request.ResourceSpans {
		for _, scope := range resource.ScopeSpans {
			count += len(scope.Spans)
		}
	}
	return count
}

func ListenAndServe(ctx context.Context, address, cert, key string, handler http.Handler) error {
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 70 * time.Second, WriteTimeout: 70 * time.Second, IdleTimeout: 2 * time.Minute}
	errCh := make(chan error, 1)
	go func() {
		if cert != "" {
			errCh <- server.ListenAndServeTLS(filepath.Clean(cert), filepath.Clean(key))
		} else {
			errCh <- server.ListenAndServe()
		}
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
