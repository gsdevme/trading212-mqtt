package mock

import "net/http"

// addRoutes is the single place mapping the mocked API surface to handlers.
// Only the two read endpoints exist: anything else 404s, which keeps the mock an
// honest stand-in for a service that must never issue a write.
func addRoutes(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("GET /api/v0/equity/account/summary", s.handleAccountSummary)
	mux.HandleFunc("GET /api/v0/equity/positions", s.handlePositions)
}
