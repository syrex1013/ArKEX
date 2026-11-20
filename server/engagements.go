package server

import (
	"errors"
	"net/http"

	"github.com/Autumn-27/artex/db"
)

// Engagement management: optional top-level grouping for tasks. Findings and
// stats derive from the engagement's tasks; deleting detaches (never deletes).
// Handlers mirror the task-categories pattern.

const maxEngagementRequestBytes = 16 << 10

type engagementRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func decodeEngagementRequest(w http.ResponseWriter, r *http.Request) (*engagementRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxEngagementRequestBytes)
	var request engagementRequest
	if err := decode(w, r, &request); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErr(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return nil, false
	}
	return &request, true
}

func writeEngagementError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrEngagementInvalid), errors.Is(err, db.ErrEngagementNameTooLong), errors.Is(err, db.ErrEngagementDescTooLong):
		writeErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, db.ErrEngagementNameConflict):
		writeErr(w, http.StatusConflict, "engagement name already exists")
	case errors.Is(err, db.ErrEngagementNotFound):
		writeErr(w, http.StatusNotFound, "engagement not found")
	case errors.Is(err, db.ErrEngagementTaskNotFound):
		writeErr(w, http.StatusNotFound, "task not found")
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

// GET /api/engagements — every engagement with live-task and finding stats.
func (s *Server) listEngagements(w http.ResponseWriter, _ *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	items, err := pg.ListEngagements()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []db.EngagementWithStats{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"engagements": items})
}

// POST /api/engagements {name, description} — create.
func (s *Server) createEngagement(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	request, ok := decodeEngagementRequest(w, r)
	if !ok {
		return
	}
	name := ""
	if request.Name != nil {
		name = *request.Name
	}
	description := ""
	if request.Description != nil {
		description = *request.Description
	}
	item, err := pg.CreateEngagement(name, description)
	if err != nil {
		writeEngagementError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

// PATCH /api/engagements/{id} {name?, description?} — edit; empty name keeps,
// empty description clears.
func (s *Server) updateEngagement(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad engagement id")
		return
	}
	request, ok := decodeEngagementRequest(w, r)
	if !ok {
		return
	}
	name, description := "", ""
	if request.Name != nil {
		name = *request.Name
	}
	if request.Description != nil {
		description = *request.Description
	}
	item, err := pg.UpdateEngagement(id, name, description)
	if err != nil {
		writeEngagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// DELETE /api/engagements/{id} — remove; tasks detach, nothing is deleted.
func (s *Server) deleteEngagement(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad engagement id")
		return
	}
	deleted, err := pg.DeleteEngagement(id)
	if err != nil {
		writeEngagementError(w, err)
		return
	}
	if !deleted {
		writeErr(w, http.StatusNotFound, "engagement not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// PATCH /api/tasks/{id}/engagement {engagement_id: number|null} — assign or
// clear a task's engagement.
func (s *Server) setTaskEngagement(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if _, ok := s.m.Task(taskID); !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxEngagementRequestBytes)
	var request struct {
		EngagementID *int64 `json:"engagement_id"`
	}
	if err := decode(w, r, &request); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.m.SetTaskEngagement(taskID, request.EngagementID); err != nil {
		writeEngagementError(w, err)
		return
	}
	task, _ := s.m.Task(taskID)
	writeJSON(w, http.StatusOK, taskDTO(task, s.resolvedTaskStatus(task)))
}
