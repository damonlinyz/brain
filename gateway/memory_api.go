package gateway

import (
	"encoding/json"
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
	json.NewEncoder(w).Encode(cc)
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
		// /nodes/{id}/correct
		if len(parts) > 1 && parts[1] == "correct" {
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
		} else {
			http.Error(w, "not found", http.StatusNotFound)
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
