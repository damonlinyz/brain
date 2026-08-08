package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/damonlinyz/brain/memory/store"
	"github.com/damonlinyz/brain/memory/types"
	memoryhub "github.com/damonlinyz/brain/memory/hub"
	"github.com/google/uuid"
)

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		RawText string `json:"raw_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.RawText == "" {
		http.Error(w, `{"error":"raw_text required"}`, http.StatusBadRequest)
		return
	}
	res, err := s.hub.Ingest(r.Context(), memoryhub.IngestInput{
		UserID:  s.ownerID(),
		RawText: body.RawText,
		Source:  types.SourceHumanInput,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"stored": len(res.Stored), "dropped": res.Dropped})
}

func (s *Server) handleRecall(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("query")
	if q == "" {
		http.Error(w, `{"error":"query required"}`, http.StatusBadRequest)
		return
	}
	cc, err := s.hub.Recall(r.Context(), memoryhub.RecallInput{UserID: s.ownerID(), Query: q})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// [#1 readable layer] ?format=markdown renders a human-readable view.
	if r.URL.Query().Get("format") == "markdown" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Write([]byte(cc.Markdown()))
		return
	}
	json.NewEncoder(w).Encode(cc)
}

func (s *Server) handleMemoryIndex(w http.ResponseWriter, r *http.Request) {
	results, err := s.hub.ListNodes(r.Context(), store.SearchFilter{UserID: s.ownerID(), Limit: 200})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	var sb strings.Builder
	sb.WriteString("# Memory Index\n\n")
	for _, n := range results.Items {
		kind := string(n.ContentType)
		if n.Type != "" { kind = string(n.Type) + "/" + kind }
		state := string(n.State)
		sb.WriteString(fmt.Sprintf("- **%s** `[%s]` %s — %s\n",
			trunc60(n.Summary), kind, state, shortTag(n)))
	}
	w.Write([]byte(sb.String()))
}

func trunc60(s string) string {
	r := []rune(s)
	if len(r) <= 60 { return s }
	return string(r[:57]) + "..."
}

func shortTag(n types.MemoryNode) string {
	if n.Weight < 0.1 { return "⚰️ extinct" }
	if n.State == types.NodeStateSuppressed { return "🟡 suppressed" }
	if n.Confidence > 0.6 { return "🟢 confident" }
	return ""
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	uid := s.ownerID()
	// GET /api/v1/memory/nodes/{id}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/api/v1/memory/nodes/"), "/", 2)
	idStr := strings.TrimSpace(parts[0])
	if idStr == "" {
		// list
		results, err := s.hub.ListNodes(r.Context(), store.SearchFilter{UserID: uid, Limit: 50})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(results.Items)
		return
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		n, err := s.hub.GetNode(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(n)
	case http.MethodDelete:
		if err := s.hub.SoftDelete(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]bool{"deleted": true})
	case http.MethodPost:
		// /nodes/{id}/correct | /nodes/{id}/pin | /nodes/{id}/unpin
		switch {
		case len(parts) > 1 && parts[1] == "correct":
			var body struct {
				Content string `json:"content"`
				Summary string `json:"summary"`
				Reason  string `json:"reason"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Content == "" {
				http.Error(w, `{"error":"content required"}`, http.StatusBadRequest)
				return
			}
			n, err := s.hub.Correct(r.Context(), id, body.Content, body.Summary, body.Reason)
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			json.NewEncoder(w).Encode(n)
		case len(parts) > 1 && parts[1] == "pin":
			// [#2 pinned-core tier] mark a node as always-inject core.
			if err := s.hub.SetTier(r.Context(), id, types.NodeTierCore); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"tier": "core"})
		case len(parts) > 1 && parts[1] == "unpin":
			if err := s.hub.SetTier(r.Context(), id, types.NodeTierNormal); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"tier": "normal"})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
