// Package gw implements the infrastructure-facing Gateway: it owns a Runtime,
// exposes the target list with live status, and bridges an interactive Session
// to a WebSocket on attach. Credentials live in the Runtime configuration and
// never cross toward the BFF or the browser.
package gw

import (
	"encoding/json"
	"net/http"

	"github.com/abyss0-dev/web-terminal/internal/runtime"
	"github.com/gorilla/websocket"
)

// wsConn is the subset of *websocket.Conn the bridge needs, isolated so the
// bridge can be tested without depending on a concrete transport.
type wsConn interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	Close() error
}

const (
	wsBinary = websocket.BinaryMessage
	wsText   = websocket.TextMessage
)

// Server exposes the Gateway's HTTP surface over a Runtime.
type Server struct {
	rt       runtime.Runtime
	upgrader websocket.Upgrader
}

// NewServer builds a Gateway HTTP server over the given Runtime.
func NewServer(rt runtime.Runtime) *Server {
	return &Server{
		rt: rt,
		// The GW trusts its sole client, the BFF; origin is not checked.
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
	}
}

// Handler returns the Gateway's HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/targets", s.handleTargets)
	mux.HandleFunc("/attach", s.handleAttach)
	return mux
}

// handleTargets returns the configured targets with live status as JSON. No
// connection details or credentials are included.
func (s *Server) handleTargets(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s.rt.Targets()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleAttach upgrades to a WebSocket, opens an interactive session to the
// requested target, and bridges the two until either side closes.
func (s *Server) handleAttach(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing target id", http.StatusBadRequest)
		return
	}

	sess, err := s.rt.Attach(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		_ = sess.Close()
		return
	}
	bridge(ws, sess)
}
