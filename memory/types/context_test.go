package types

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCompressedContext_Markdown_Empty(t *testing.T) {
	got := CompressedContext{}.Markdown()
	if !strings.Contains(got, "0 items") {
		t.Fatalf("empty header should mention 0 items, got: %s", got)
	}
	if !strings.Contains(got, "No memories") {
		t.Fatalf("empty body should say no memories, got: %s", got)
	}
}

func TestCompressedContext_Markdown_RendersProvenance(t *testing.T) {
	cc := CompressedContext{
		TokenBudget:  1000,
		TokenUsed:    120,
		LiveInjectOK: true,
		Memories: []MemoryRef{
			{NodeID: uuid.New(), Summary: "likes dark mode", Relevance: 0.82, Source: "vector"},
			{NodeID: uuid.New(), Summary: "colour scheme talk", Relevance: 0.5, Source: "graph", Detail: "similar"},
			{NodeID: uuid.New(), Summary: "name: damon", Relevance: 1.0, Source: "core", Tier: "core"},
		},
	}
	got := cc.Markdown()
	checks := []string{
		"3 items",        // header count (plural)
		"120/1000 tokens", // budget line
		"inject=true",
		"vector",         // source tag
		"82%",            // relevance percent
		"graph·similar",  // graph detail joined
		"📌",             // core tier mark
		"likes dark mode",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("markdown missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestCompressedContext_Markdown_SingularItem(t *testing.T) {
	cc := CompressedContext{
		Memories: []MemoryRef{{Summary: "solo", Relevance: 0.5}},
	}
	if !strings.Contains(cc.Markdown(), "1 item ") {
		t.Fatalf("single item should use singular 'item', got: %s", cc.Markdown())
	}
}
