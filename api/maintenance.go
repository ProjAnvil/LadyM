package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
)

func (h *Handler) handleScheduleMaintenance(w http.ResponseWriter, r *http.Request) {
	var req schema.ScheduleMaintenanceRequest
	if err := decodeClaimBody(w, r, &req); err != nil {
		writeError(w, 400, "invalid maintenance request")
		return
	}
	m, err := h.eng.WithContext(r.Context()).Scope(h.effectiveWS(r, req.Workspace)).ScheduleMaintenance(req.Operation, req.TargetID, req.InputIDs, req.Args)
	if err != nil {
		if errors.Is(err, engine.ErrInvalidMaintenance) {
			writeError(w, 400, err.Error())
		} else {
			claimError(w, err)
		}
		return
	}
	writeJSON(w, 200, m)
}

func (h *Handler) handleMaintenanceJobs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if text := r.URL.Query().Get("limit"); text != "" {
		value, err := strconv.Atoi(text)
		if err != nil {
			writeError(w, 400, "invalid limit")
			return
		}
		limit = value
	}
	jobs, err := h.eng.WithContext(r.Context()).Scope(h.effectiveWS(r, r.URL.Query().Get("workspace"))).ListMaintenanceJobs(limit)
	if err != nil {
		if errors.Is(err, engine.ErrInvalidMaintenance) {
			writeError(w, 400, err.Error())
		} else {
			claimError(w, err)
		}
		return
	}
	writeJSON(w, 200, map[string]any{"jobs": jobs})
}

func (h *Handler) handleMaintenanceStatus(w http.ResponseWriter, r *http.Request) {
	report, err := h.eng.WithContext(r.Context()).Scope(h.effectiveWS(r, r.URL.Query().Get("workspace"))).MaintenanceStatus()
	if err != nil {
		claimError(w, err)
		return
	}
	writeJSON(w, 200, report)
}
