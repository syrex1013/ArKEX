package server

import (
	"net/http"

	"github.com/Autumn-27/artex/storage"
)

// getSystemStorage returns the Storage Governor breakdown.
// GET /api/system/storage
func (s *Server) getSystemStorage(w http.ResponseWriter, r *http.Request) {
	gov := s.storageGov
	if gov == nil && s.m != nil && s.m.pg != nil {
		gov = storage.NewGovernor(s.m.pg.DB, s.m.dir)
	}
	if gov == nil {
		writeErr(w, 503, "storage governor unavailable")
		return
	}
	b, err := gov.Breakdown(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, b)
}
