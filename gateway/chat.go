package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	memoryhub "github.com/damonlinyz/brain/memory/hub"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

type chatRequest struct {
	Model    string          `json:"model"`
	Messages []chatMessage   `json:"messages"`
	Stream   bool            `json:"stream"`
	Tools    json.RawMessage `json:"tools,omitempty"`
}
type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid: "+err.Error(), http.StatusBadRequest)
		return
	}
	req = s.injectMemory(r.Context(), req)
	b2, _ := json.Marshal(req)
	s.forwardUpstream(w, r, b2, "/v1/chat/completions")
}

func (s *Server) handleAnthropicChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err == nil {
		uid := s.ownerID()
		if uid != uuid.Nil {
			if q := lastUserText(raw); q != "" {
				if p, n, _ := s.recallPrompt(r.Context(), uid, q); p != "" {
					raw["system"] = prependSystem(raw["system"], "## Relevant Memories (V2)\n"+p)
					s.log.Info("v2 memory injected (anthropic)", "recalled", n)
				}
			}
		}
	}
	b2, _ := json.Marshal(raw)
	s.forwardUpstream(w, r, b2, "/v1/messages")
}

func (s *Server) injectMemory(ctx context.Context, req chatRequest) chatRequest {
	uid := s.ownerID()
	if uid == uuid.Nil { return req }
	query := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" { query = rawText(req.Messages[i].Content); break }
	}
	if query == "" { return req }
	prompt, n, err := s.recallPrompt(ctx, uid, query)
	if err != nil || prompt == "" { return req }
	frag := "## Relevant Memories (V2)\n" + prompt
	s.log.Info("v2 memory injected", "recalled", n, "query_len", len(query))
	for i := range req.Messages {
		if req.Messages[i].Role == "system" {
			req.Messages[i].Content = mustJSON(frag + "\n\n" + rawText(req.Messages[i].Content))
			s.ingestAsync(uid, query)
			return req
		}
	}
	req.Messages = append([]chatMessage{{Role: "system", Content: mustJSON(frag)}}, req.Messages...)
	s.ingestAsync(uid, query)
	return req
}

func (s *Server) recallPrompt(ctx context.Context, uid uuid.UUID, q string) (string, int, error) {
	cc, err := s.hub.Recall(ctx, memoryhub.RecallInput{UserID: uid, Query: q})
	if err != nil { return "", 0, err }
	return cc.SystemPrompt, len(cc.Memories), nil
}

func (s *Server) ingestAsync(uid uuid.UUID, raw string) {
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := s.hub.Ingest(bg, memoryhub.IngestInput{UserID: uid, RawText: raw, Source: types.SourceHumanInput}); err != nil {
			s.log.Debug("v2 ingest failed", "error", err)
		}
	}()
}

func (s *Server) forwardUpstream(w http.ResponseWriter, r *http.Request, body []byte, path string) {
	url := strings.TrimRight(s.cfg.UpstreamLLMBaseURL, "/") + path
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.cfg.UpstreamLLMAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.UpstreamLLMAPIKey)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		s.log.Error("upstream failed", "error", err)
		http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs { w.Header().Add(k, v) }
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (s *Server) ownerID() uuid.UUID {
	id, _ := uuid.Parse(s.cfg.UserID)
	return id
}

func mustJSON(s string) json.RawMessage { b, _ := json.Marshal(s); return b }

func rawText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil { return s }
	return strings.Trim(string(raw), `"`)
}

func lastUserText(raw map[string]any) string {
	msgs, _ := raw["messages"].([]any)
	for i := len(msgs) - 1; i >= 0; i-- {
		m, _ := msgs[i].(map[string]any)
		if r, _ := m["role"].(string); r == "user" {
			switch c := m["content"].(type) {
			case string: return c
			case []any:
				var sb strings.Builder
				for _, blk := range c {
					if bm, _ := blk.(map[string]any); bm != nil {
						if t, _ := bm["text"].(string); t != "" { sb.WriteString(t) }
					}
				}
				return sb.String()
			}
		}
	}
	return ""
}

func prependSystem(existing any, prefix string) any {
	switch e := existing.(type) {
	case string: return prefix + "\n\n" + e
	case []any: return append([]any{map[string]any{"type":"text","text":prefix}}, e...)
	case nil: return prefix
	default: return prefix
	}
}
