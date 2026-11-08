package api

import (
	"net/http"

	"github.com/ProjAnvil/LadyM/schema"
)

func (h *Handler) handleBuildTopic(w http.ResponseWriter, r *http.Request) {
	var req schema.BuildTopicRequest
	if err := decodeClaimBody(w, r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	m, err := h.eng.Scope(h.effectiveWS(r, req.Workspace)).BuildTopic(req.Topic, req.InputIDs, req.ExpectedRevision)
	if err != nil {
		claimError(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (h *Handler) handleGetTopic(w http.ResponseWriter, r *http.Request) {
	m, err := h.eng.Scope(h.effectiveWS(r, r.URL.Query().Get("workspace"))).GetTopic(r.PathValue("topic"))
	if err != nil {
		claimError(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (h *Handler) handleHybridRecall(w http.ResponseWriter, r *http.Request) {
	var req schema.HybridRecallRequest
	if err := decodeClaimBody(w, r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	m, err := h.eng.Scope(h.effectiveWS(r, req.Workspace)).HybridRecall(req.Query, req.Limit)
	if err != nil {
		claimError(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (h *Handler) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	var req schema.BootstrapRequest
	if err := decodeClaimBody(w, r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if req.Unit != "" && req.Unit != "bytes" {
		writeError(w, 400, "remote bootstrap supports bytes only; tokens require a host TokenCounter")
		return
	}
	m, err := h.eng.Scope(h.effectiveWS(r, req.Workspace)).Bootstrap(req, nil)
	if err != nil {
		claimError(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (h *Handler) handleSetTaskState(w http.ResponseWriter, r *http.Request) {
	var req schema.SetTaskStateRequest
	if err := decodeClaimBody(w, r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	m, err := h.eng.Scope(h.effectiveWS(r, req.Workspace)).SetTaskState(req.ID, req.Goal, req.Next, req.TTLS)
	if err != nil {
		claimError(w, err)
		return
	}
	writeJSON(w, 200, m)
}
