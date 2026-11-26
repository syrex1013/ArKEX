package server

// Review-state HTTP surface for project knowledge. One mutation: a human
// verdict on an existing item (still trusted / disputed / due for revalidation).
// Reads stay on the swarm read route (GET /api/tasks/{id}/knowledge).

import (
	"errors"
	"net/http"

	"github.com/Autumn-27/artex/db"
)

// knowledgeReviewRequest is the PATCH /api/knowledge/{id} body.
//
// review_state is required and must be one of db's three states. revalidate_after
// (RFC3339) is accepted so a client can send the full item back unchanged, but
// the store has no setter for it yet: writing it from here would mean raw SQL in
// the HTTP layer, so the field is parsed by the JSON decoder and otherwise
// ignored until db grows the method.
type knowledgeReviewRequest struct {
	ReviewState     string `json:"review_state"`
	RevalidateAfter string `json:"revalidate_after"`
}

// updateKnowledge serves PATCH /api/knowledge/{id}: record whether operators
// still trust an item.
//
// Unknown ids 404 (checked before the state so a bogus id is never reported as a
// bad payload); a state outside the three the schema knows is 400 carrying the
// store's own message; success returns the refreshed item, which is what the UI
// re-renders with (review_state may be normalised by the store).
func (s *Server) updateKnowledge(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, http.StatusBadRequest, "bad knowledge id")
		return
	}
	var req knowledgeReviewRequest
	if err := decode(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	item, err := pg.GetKnowledge(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item == nil {
		writeErr(w, http.StatusNotFound, "knowledge item not found")
		return
	}
	if err := pg.SetKnowledgeReviewState(id, req.ReviewState); err != nil {
		if errors.Is(err, db.ErrKnowledgeReviewState) {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	item, err = pg.GetKnowledge(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"knowledge": item})
}
