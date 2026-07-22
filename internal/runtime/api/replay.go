package api

import (
	"net/http"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func (s *Server) createReplay(writer http.ResponseWriter, request *http.Request) {
	body, err := decodeReplayRequest(request)
	if err != nil {
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

func (s *Server) preflightReplay(writer http.ResponseWriter, request *http.Request) {
	body, err := decodeReplayRequest(request)
	if err != nil {
		writeError(writer, err)
		return
	}
	report, err := s.replays.Check(request.Context(), body)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, report)
}

func decodeReplayRequest(request *http.Request) (types.CreateReplayRequest, error) {
	body := types.CreateReplayRequest{SubstituteLLM: true}
	if err := decodeJSON(request, &body); err != nil {
		return types.CreateReplayRequest{}, err
	}
	return body, nil
}
