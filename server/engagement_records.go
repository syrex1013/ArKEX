package server

// Engagement record endpoints: the append-only engagement history (scope
// revisions, operator decisions, milestones, briefs, notes), the unioned
// timeline, per-operator review cursors and the deterministic brief.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/jackc/pgx/v5/pgconn"
)

const maxRecordRequestBytes = 64 << 10

type recordRequest struct {
	Kind         *string         `json:"kind"`
	Title        *string         `json:"title"`
	Body         *string         `json:"body"`
	Actor        *string         `json:"actor"`
	Status       *string         `json:"status"`
	DueAt        *time.Time      `json:"due_at"`
	Criteria     *string         `json:"criteria"`
	SupersedesID *int64          `json:"supersedes_id"`
	SourceKind   *string         `json:"source_kind"`
	SourceID     *int64          `json:"source_id"`
	Payload      json.RawMessage `json:"payload"`
}

func writeRecordError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrEngagementRecordInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, db.ErrEngagementRecordNotFound):
		writeErr(w, http.StatusNotFound, "record not found")
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, http.StatusNotFound, "engagement not found")
	default:
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			writeErr(w, http.StatusNotFound, "engagement or referenced row not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

func csvKinds(raw string) []string {
	if raw == "" {
		return nil
	}
	var kinds []string
	for _, k := range strings.Split(raw, ",") {
		if k = strings.TrimSpace(k); k != "" {
			kinds = append(kinds, k)
		}
	}
	return kinds
}

// GET /api/engagements/{id}/records?kinds=&status=&since=&limit=
func (s *Server) listEngagementRecords(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad engagement id")
		return
	}
	q := r.URL.Query()
	sinceID, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	limit := atoiDefault(q.Get("limit"), 100)
	records, err := pg.ListEngagementRecords(id, csvKinds(q.Get("kinds")), q.Get("status"), sinceID, limit)
	if err != nil {
		writeRecordError(w, err)
		return
	}
	if records == nil {
		records = []db.EngagementRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": records})
}

// POST /api/engagements/{id}/records — append one record.
func (s *Server) createEngagementRecord(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad engagement id")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRecordRequestBytes)
	var request recordRequest
	if err := decode(w, r, &request); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rec := &db.EngagementRecord{}
	if request.Kind != nil {
		rec.Kind = *request.Kind
	}
	if request.Title != nil {
		rec.Title = *request.Title
	}
	if request.Body != nil {
		rec.Body = *request.Body
	}
	if request.Actor != nil {
		rec.Actor = *request.Actor
	}
	if request.Status != nil {
		rec.Status = *request.Status
	}
	rec.DueAt = request.DueAt
	if request.Criteria != nil {
		rec.Criteria = *request.Criteria
	}
	rec.SupersedesID = request.SupersedesID
	if request.SourceKind != nil {
		rec.SourceKind = *request.SourceKind
	}
	rec.SourceID = request.SourceID
	if len(request.Payload) > 0 {
		rec.Payload = request.Payload
	}
	created, err := pg.CreateEngagementRecord(id, rec)
	if err != nil {
		writeRecordError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// PATCH /api/engagements/records/{rid} {status} — the only mutable field;
// everything else is append-only history.
func (s *Server) updateEngagementRecord(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	rid, ok := pathInt(r, "rid")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad record id")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRecordRequestBytes)
	var request struct {
		Status string `json:"status"`
	}
	if err := decode(w, r, &request); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := pg.UpdateEngagementRecordStatus(rid, request.Status)
	if err != nil {
		writeRecordError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// GET /api/engagements/{id}/timeline?since=RFC3339&kinds=csv&limit=
func (s *Server) engagementTimeline(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad engagement id")
		return
	}
	q := r.URL.Query()
	var since time.Time
	if raw := q.Get("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "since must be RFC3339")
			return
		}
		since = parsed
	}
	limit := atoiDefault(q.Get("limit"), 100)
	entries, err := pg.EngagementTimeline(id, since, csvKinds(q.Get("kinds")), limit)
	if err != nil {
		writeRecordError(w, err)
		return
	}
	if entries == nil {
		entries = []db.EngagementTimelineEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// POST /api/engagements/{id}/review {"operator": "..."} — stamp the review
// cursor and return the diff set that was just reviewed ("changes since my
// last review").
func (s *Server) reviewEngagement(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad engagement id")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRecordRequestBytes)
	var request struct {
		Operator string `json:"operator"`
	}
	if err := decode(w, r, &request); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	operator := strings.TrimSpace(request.Operator)
	if operator == "" {
		operator = "operator"
	}

	// Delta relative to the operator's previous cursor.
	var sinceTime time.Time
	var sinceID int64
	if prev, err := pg.LatestEngagementReview(id, operator); err != nil {
		writeRecordError(w, err)
		return
	} else if prev != nil {
		sinceTime = prev.ReviewedAt
		sinceID = prev.UpToRecordID
	}
	records, err := pg.RecordsSinceID(id, sinceID)
	if err != nil {
		writeRecordError(w, err)
		return
	}
	if records == nil {
		records = []db.EngagementRecord{}
	}
	entries, err := pg.EngagementTimeline(id, sinceTime, nil, 100)
	if err != nil {
		writeRecordError(w, err)
		return
	}
	if entries == nil {
		entries = []db.EngagementTimelineEntry{}
	}

	maxID, err := pg.MaxRecordID(id)
	if err != nil {
		writeRecordError(w, err)
		return
	}
	review, err := pg.AddEngagementReview(id, operator, maxID)
	if err != nil {
		writeRecordError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"review":  review,
		"records": records,
		"entries": entries,
	})
}

// GET /api/engagements/{id}/brief?operator= — deterministic handoff brief.
// With operator set the brief carries that operator's review cursor and the
// records added since it.
func (s *Server) engagementBrief(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad engagement id")
		return
	}
	brief, err := pg.EngagementBrief(r.Context(), id, strings.TrimSpace(r.URL.Query().Get("operator")))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "engagement not found")
			return
		}
		writeRecordError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"brief": brief})
}
