package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cordon-dev/cordon"
)

// Server exposes a Cordon Sandbox over HTTP/JSON for local or remote agent integration.
type Server struct {
	sb *cordon.Sandbox
}

// NewHandler constructs an http.Handler serving the Cordon HTTP RPC API for the given Sandbox.
func NewHandler(sb *cordon.Sandbox) http.Handler {
	s := &Server{sb: sb}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /v1/tools", s.handleListTools)
	mux.HandleFunc("POST /v1/tools/call", s.handleCallTool)
	mux.HandleFunc("POST /v1/exec", s.handleExecBash)

	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListTools(w http.ResponseWriter, r *http.Request) {
	tools := s.sb.Tools()
	writeJSON(w, http.StatusOK, map[string]any{
		"tools": tools,
	})
}

type callToolRequest struct {
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"` // OpenAI compatibility
}

func (s *Server) handleCallTool(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req callToolRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON payload: %v", err))
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "missing required 'name' field")
		return
	}

	input := req.Input
	if len(input) == 0 {
		input = req.Arguments
	}
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}

	// If input is encoded as a JSON string, decode the inner JSON
	var rawStr string
	if err := json.Unmarshal(input, &rawStr); err == nil {
		input = json.RawMessage(rawStr)
	}

	res, err := s.sb.CallTool(r.Context(), req.Name, input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, res)
}

type execBashRequest struct {
	Command string `json:"command"`
}

func (s *Server) handleExecBash(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req execBashRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON payload: %v", err))
		return
	}

	res, err := s.sb.ExecBash(r.Context(), req.Command)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, res)
}

func writeJSON(w http.ResponseWriter, statusCode int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, statusCode int, msg string) {
	writeJSON(w, statusCode, map[string]any{
		"error":    strings.TrimSpace(msg),
		"is_error": true,
	})
}
