package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/runtime/access"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
	flightversion "github.com/krishnangopal1810/ai-flight-recorder/internal/version"
)

func (s *Server) hydrateDiffValues(ctx context.Context, result *types.DiffResult) {
	for index := range result.Observations {
		s.hydrateObservation(ctx, &result.Observations[index], result.BaselineCapsuleID, result.CandidateCapsuleID)
	}
	if result.FirstDivergence != nil {
		s.hydrateObservation(ctx, result.FirstDivergence, result.BaselineCapsuleID, result.CandidateCapsuleID)
	}
}

func (s *Server) hydrateObservation(ctx context.Context, observation *types.Observation, baselineID, candidateID string) {
	if observation.Field != "input_digest" && observation.Field != "output_digest" {
		return
	}
	for _, evidence := range observation.Evidence {
		if evidence.Payload == "" {
			continue
		}
		data, err := s.capsules.Payload(ctx, evidence.Payload)
		if err != nil || len(data) > 64<<10 {
			continue
		}
		var value any
		if json.Unmarshal(data, &value) != nil {
			value = string(data)
		}
		if evidence.CapsuleID == baselineID {
			observation.BeforeValue = value
		}
		if evidence.CapsuleID == candidateID {
			observation.AfterValue = value
		}
	}
}

func (s *Server) session(writer http.ResponseWriter, request *http.Request) {
	info := types.SessionInfo{
		SchemaVersion:      types.SchemaVersion,
		ServiceVersion:     types.SchemaVersion,
		ReleaseVersion:     flightversion.Release,
		AuthMode:           "token",
		LocalModeAvailable: s.config.Auth.LocalBypass,
		Scopes:             []string{},
	}
	if s.config.Auth.LocalBypass {
		info.AuthMode, info.Connected = "local_bypass", true
		info.Scopes = []string{"admin", "ingest", "read", "replay"}
		writeJSON(writer, http.StatusOK, info)
		return
	}
	values := request.Header.Values("Authorization")
	if len(values) == 0 {
		writeJSON(writer, http.StatusOK, info)
		return
	}
	value, err := access.BearerToken(values)
	if err != nil {
		writeError(writer, err)
		return
	}
	scopes, err := s.auth.Scopes(request.Context(), value)
	if err != nil {
		writeError(writer, err)
		return
	}
	sort.Strings(scopes)
	info.Connected, info.Scopes = true, scopes
	writeJSON(writer, http.StatusOK, info)
}
