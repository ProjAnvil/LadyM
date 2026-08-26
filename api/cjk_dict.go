// CJK dictionary endpoints for the console's settings page: status,
// on-demand download, and removal. The dictionary lives at the storage
// layer, so these work identically in both editions; download/remove are
// admin-only system mutations, status is readable by any authenticated
// user.

package api

import (
	"fmt"
	"net/http"

	"github.com/ProjAnvil/LadyM/storage"
)

// handleCJKDictStatus reports the active dictionary state plus the
// downloadable variants (for the settings-UI dropdown).
func (h *Handler) handleCJKDictStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   storage.CJKDictStatusNow(),
		"variants": storage.CJKDictVariants(),
	})
}

// handleCJKDictDownload downloads a dictionary variant into the configured
// dict dir and reloads the segmenter on every instance sharing that dir.
// Body (all fields optional):
//
//	{"dict": "zh"|"zh_s"|"zh_t"|"jp", "mirror_base": "https://internal/"}
//
// mirror_base replaces the default mirror list for air-gapped installs that
// replicate the layout at an internal mirror (<base>/<rel_path>).
func (h *Handler) handleCJKDictDownload(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	var body struct {
		Dict       string `json:"dict"`
		MirrorBase string `json:"mirror_base"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	name := storage.CJKDictName(body.Dict)
	if name != "" {
		valid := false
		for _, v := range storage.CJKDictVariants() {
			if v.Name == name {
				valid = true
				break
			}
		}
		if !valid {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("unknown dict %q; see GET /api/cjk_dict variants", body.Dict))
			return
		}
	}
	st, err := storage.DownloadCJKDictTo(name, "", body.MirrorBase)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": st})
}

// handleCJKDictRemove deletes the downloaded dictionary and falls back to
// the embedded one (fulldict builds) or per-character tokenization.
func (h *Handler) handleCJKDictRemove(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if err := storage.RemoveCJKDict(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, storage.CJKDictStatusNow())
}
