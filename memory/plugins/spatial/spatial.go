// Package spatial implements the G2 Spatial plugin — binds memory nodes to
// conversation sessions and enables lookup-by-session. This is how the system
// answers "what did we talk about in that session?"
package spatial

import (
	"context"
	"sync"

	"github.com/damonlinyz/brain/memory/store"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

type Plugin struct{ mu sync.RWMutex }

func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string                   { return "Spatial" }
func (p *Plugin) Category() types.PluginCategory { return types.CategoryEngine }
func (p *Plugin) Init(cfg map[string]any) error   { return nil }
func (p *Plugin) Close() error                    { return nil }

// BindSession sets the session_id on a node, linking it to a conversation.
func (p *Plugin) BindSession(ctx context.Context, s store.IMemoryStore, nodeID, sessionID uuid.UUID) error {
	node, err := s.GetNode(ctx, nodeID)
	if err != nil {
		return err
	}
	_, err = s.UpdateNode(ctx, nodeID, node.Version, func(n *types.MemoryNode) {
		n.SessionID = &sessionID
	})
	return err
}

// ListBySession returns all nodes linked to a session.
func (p *Plugin) ListBySession(ctx context.Context, s store.IMemoryStore, userID, sessionID uuid.UUID) ([]types.MemoryNode, error) {
	results, err := s.ListNodes(ctx, store.SearchFilter{
		UserID:    userID,
		SessionID: &sessionID,
		Limit:     200,
	})
	if err != nil {
		return nil, err
	}
	return results.Items, nil
}
