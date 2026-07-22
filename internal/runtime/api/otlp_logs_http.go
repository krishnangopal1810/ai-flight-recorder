package api

import (
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"strings"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/providers/otlpjson"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func (s *Server) otlpLogsHTTP(writer http.ResponseWriter, request *http.Request) {
	data, jsonEncoding, err := readOTLPRequest(writer, request)
	if err != nil {
		writeError(writer, err)
		return
	}
	var exportRequest collectorlogs.ExportLogsServiceRequest
	if jsonEncoding {
		data, err = otlpjson.NormalizeIDs(data)
		if err == nil {
			err = protojson.Unmarshal(data, &exportRequest)
		}
	} else {
		err = proto.Unmarshal(data, &exportRequest)
	}
	if err != nil {
		writeError(writer, types.NewError(types.ErrValidation, "malformed OTLP logs request", "send a valid ExportLogsServiceRequest", err))
		return
	}
	rejected, captureErr := s.receiver.CaptureLogs(request.Context(), &exportRequest)
	response := &collectorlogs.ExportLogsServiceResponse{}
	if rejected > 0 || captureErr != nil {
		response.PartialSuccess = &collectorlogs.ExportLogsPartialSuccess{RejectedLogRecords: int64(rejected), ErrorMessage: safeMessage(captureErr)}
	}
	var encoded []byte
	if jsonEncoding {
		encoded, err = protojson.Marshal(response)
	} else {
		encoded, err = proto.Marshal(response)
	}
	if err != nil {
		writeError(writer, err)
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

func readOTLPRequest(writer http.ResponseWriter, request *http.Request) ([]byte, bool, error) {
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	jsonEncoding := mediaType == "application/json"
	if mediaType != "application/x-protobuf" && mediaType != "application/protobuf" && !jsonEncoding {
		return nil, false, types.NewError(types.ErrValidation, "OTLP HTTP content type is unsupported", "send application/x-protobuf or application/json", nil)
	}
	var body io.Reader = http.MaxBytesReader(writer, request.Body, 64<<20)
	if strings.EqualFold(request.Header.Get("Content-Encoding"), "gzip") {
		compressed, err := gzip.NewReader(body)
		if err != nil {
			return nil, false, types.NewError(types.ErrValidation, "invalid OTLP gzip body", "send a valid gzip stream", err)
		}
		defer compressed.Close()
		body = io.LimitReader(compressed, 64<<20)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, false, types.NewError(types.ErrValidation, "cannot read OTLP request", "reduce the batch size", err)
	}
	return data, jsonEncoding, nil
}
