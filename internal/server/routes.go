package server

import "net/http"

// addRoutes is the single place mapping the daemon's HTTP surface to handlers.
// GET /{$} matches the exact root only, so unknown paths 404 instead of being
// swallowed by a catch-all.
func addRoutes(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("GET /healthz", s.handleLivez)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
}
