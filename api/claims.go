package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func claimError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, engine.ErrMemoryNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, engine.ErrInvalidClaim), errors.Is(err, storage.ErrMemoryQuota):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, storage.ErrMemoryConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		engineError(w, err)
	}
}

func (h *Handler) claimActor(r *http.Request) string {
	if u, ok := r.Context().Value(ctxAuthUser).(*schema.User); ok && u != nil {
		return u.Username
	}
	return "local"
}

func decodeClaimBody(w http.ResponseWriter, r *http.Request, body any) error {
	// Bound the JSON envelope too; the engine separately caps raw content bytes.
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("invalid claim request: %w", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return fmt.Errorf("claim request must be a JSON object")
	}
	return json.Unmarshal(data, body)
}

func (h *Handler) handleRecordEvidence(w http.ResponseWriter, r *http.Request) {
	h.recordEvidence(w, r, false)
}

func (h *Handler) handleArchiveEvidence(w http.ResponseWriter, r *http.Request) {
	h.recordEvidence(w, r, true)
}

func (h *Handler) recordEvidence(w http.ResponseWriter, r *http.Request, archive bool) {
	var body schema.RecordEvidenceRequest
	if err := decodeClaimBody(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	scope := h.eng.Scope(h.effectiveWS(r, body.Workspace))
	var m *schema.Memory
	var err error
	if archive {
		m, err = scope.ArchiveEvidence(body.Kind, body.Content, body.Outcome, h.claimActor(r))
	} else {
		m, err = scope.RecordEvidence(body.Kind, body.Content, body.Outcome, h.claimActor(r))
	}
	if err != nil {
		claimError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *Handler) handleCreateClaim(w http.ResponseWriter, r *http.Request) {
	var body schema.CreateClaimRequest
	if err := decodeClaimBody(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	c, err := h.eng.Scope(h.effectiveWS(r, body.Workspace)).CreateClaim(body.Content, body.Evidence, body.Scope, body.Status, body.ValidUntil)
	if err != nil {
		claimError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handler) handleGetClaim(w http.ResponseWriter, r *http.Request) {
	c, err := h.eng.Scope(h.effectiveWS(r, r.URL.Query().Get("workspace"))).GetClaim(r.PathValue("id"))
	if err != nil {
		claimError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handler) handleClaimFeedback(w http.ResponseWriter, r *http.Request) {
	var body schema.ClaimFeedbackRequest
	if err := decodeClaimBody(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.ExpectedRevision < 1 {
		writeError(w, http.StatusBadRequest, "expected_revision must be a positive integer")
		return
	}
	body.Actor = h.claimActor(r)
	c, err := h.eng.Scope(h.effectiveWS(r, body.Workspace)).ApplyClaimFeedback(r.PathValue("id"), body.ClaimFeedback)
	if err != nil {
		claimError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}
