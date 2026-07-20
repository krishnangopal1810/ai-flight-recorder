package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /v1/chat/completions", deterministic("model", map[string]any{"id": "fake-chat", "model": "flight-fake-v1", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "deterministic model response"}, "finish_reason": "stop"}}}))
	mux.HandleFunc("POST /mcp", deterministic("mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "deterministic MCP result"}}}}))
	mux.HandleFunc("POST /api", deterministic("api", map[string]any{"ok": true, "value": "deterministic API result"}))
	server := &http.Server{Addr: ":9099", Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: time.Minute}
	logger.Info("deterministic dependency server started", "address", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("deterministic dependency server stopped", "error_type", "listen")
		os.Exit(1)
	}
}

func deterministic(kind string, body map[string]any) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 1<<20))
		if err != nil {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		digest := sha256.Sum256(data)
		response := map[string]any{"kind": kind, "request_digest": "sha256:" + hex.EncodeToString(digest[:]), "response": body}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(response)
	}
}
