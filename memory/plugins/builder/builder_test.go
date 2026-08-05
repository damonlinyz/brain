package builder

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/damonlinyz/brain/memory/types"
)

// fakeLLM implements LLMClient.
type fakeLLM struct {
	available bool
	reply     string
	err       error
	delay     time.Duration
}

func (f *fakeLLM) Available() bool { return f.available }
func (f *fakeLLM) AvailableTrue()  { f.available = true }
func (f *fakeLLM) Chat(system, user string) (string, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.err != nil {
		return "", f.err
	}
	return f.reply, nil
}

func TestExtract_ParsesLLMJSON(t *testing.T) {
	p := New()
	p.AttachLLM(&fakeLLM{
		available: true,
		reply: `{
			"facts": [
				{"content":"User is vegan","summary":"vegan diet","content_type":"preference","keywords":["vegan"],"type":"profile","salience":"high"},
				{"content":"User has peanut allergy","summary":"peanut allergy","content_type":"fact","keywords":["allergy"],"type":"profile"}
			]
		}`,
	})

	out, err := p.Extract(context.Background(), ExtractInput{RawText: "I'm vegan and allergic to peanuts"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 facts, got %d (%+v)", len(out), out)
	}
	if out[0].Content != "User is vegan" {
		t.Errorf("fact[0] content: %q", out[0].Content)
	}
	if out[0].ContentType != types.ContentTypePreference {
		t.Errorf("fact[0] type: %v", out[0].ContentType)
	}
	if out[0].Type != types.MemoryTypeProfile {
		t.Errorf("fact[0] memtype: %v", out[0].Type)
	}
	if out[0].Salience != types.SalienceHigh {
		t.Errorf("fact[0] salience: %v", out[0].Salience)
	}
}

func TestExtract_StripsCodeFence(t *testing.T) {
	p := New()
	p.AttachLLM(&fakeLLM{
		available: true,
		reply: "```json\n" + `{"facts":[{"content":"Loves hiking","type":"profile"}]}` + "\n```",
	})

	out, _ := p.Extract(context.Background(), ExtractInput{RawText: "anything"})
	if len(out) != 1 || out[0].Content != "Loves hiking" {
		t.Fatalf("code-fence strip failed: %+v", out)
	}
}

func TestExtract_ToleratesPreamble(t *testing.T) {
	p := New()
	p.AttachLLM(&fakeLLM{
		available: true,
		reply: "Here you go:\n" + `{"facts":[{"content":"Likes Go","type":"semantic"}]}` + "\nThanks!",
	})

	out, _ := p.Extract(context.Background(), ExtractInput{RawText: "x"})
	if len(out) != 1 || out[0].Content != "Likes Go" {
		t.Fatalf("preamble tolerance failed: %+v", out)
	}
}

func TestExtract_FallbackOnLLMUnavailable(t *testing.T) {
	p := New()
	// No LLM attached.
	out, err := p.Extract(context.Background(), ExtractInput{
		RawText: "I love pizza. I also play piano.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 fallback facts, got %d", len(out))
	}
	for _, f := range out {
		if f.Type != types.MemoryTypeSemantic {
			t.Errorf("fallback should default to semantic, got %v", f.Type)
		}
		if f.ContentType != types.ContentTypeFact {
			t.Errorf("fallback content type: %v", f.ContentType)
		}
	}
}

func TestExtract_FallbackOnLLMError(t *testing.T) {
	p := New()
	p.AttachLLM(&fakeLLM{available: true, err: errors.New("500 internal")})
	out, err := p.Extract(context.Background(), ExtractInput{RawText: "I love pizza."})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("expected fallback to produce 1 fact, got %d", len(out))
	}
}

func TestExtract_NoFallbackOnLLMError(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"fallbackOnLLMFail": false})
	p.AttachLLM(&fakeLLM{available: true, err: errors.New("boom")})
	_, err := p.Extract(context.Background(), ExtractInput{RawText: "I love pizza."})
	if err == nil {
		t.Fatal("expected error to propagate when fallback disabled")
	}
}

func TestExtract_RespectsMaxFacts(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"maxFactsPerTurn": 2})

	p.AttachLLM(&fakeLLM{
		available: true,
		reply: `{"facts":[
			{"content":"a"},{"content":"b"},{"content":"c"},{"content":"d"}
		]}`,
	})
	out, _ := p.Extract(context.Background(), ExtractInput{RawText: "..."})
	if len(out) != 2 {
		t.Fatalf("expected maxFacts=2 cap, got %d", len(out))
	}
}

func TestExtract_EmptyInputReturnsNil(t *testing.T) {
	p := New()
	out, err := p.Extract(context.Background(), ExtractInput{RawText: "   "})
	if err != nil || out != nil {
		t.Fatalf("expected nil for blank input, got %v %v", out, err)
	}
}

func TestExtract_LLMReturnsMalformedJSON_FallsBack(t *testing.T) {
	p := New()
	p.AttachLLM(&fakeLLM{available: true, reply: "not json at all"})
	out, _ := p.Extract(context.Background(), ExtractInput{RawText: "I love pizza."})
	if len(out) != 1 {
		t.Fatalf("expected fallback to kick in on malformed json, got %d", len(out))
	}
}

func TestExtract_LLMTimeoutFallsBack(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"llmTimeoutMs": 50})
	p.AttachLLM(&fakeLLM{available: true, delay: 300 * time.Millisecond})

	start := time.Now()
	out, err := p.Extract(context.Background(), ExtractInput{RawText: "I love pizza."})
	dur := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if dur > 200*time.Millisecond {
		t.Fatalf("fallback should not wait the full llm delay, took %s", dur)
	}
	if len(out) != 1 {
		t.Fatalf("expected fallback fact, got %d", len(out))
	}
}

func TestFallbackExtract_SkipShortSentences(t *testing.T) {
	out := fallbackExtract(ExtractInput{RawText: "ok. I really love hiking in the mountains. hi."}, 10)
	if len(out) != 1 {
		t.Fatalf("expected short sentences filtered, got %d (%+v)", len(out), out)
	}
	if !strings.Contains(out[0].Content, "hiking") {
		t.Fatalf("unexpected content: %v", out[0].Content)
	}
}

func TestSplitSentences_CNAndEN(t *testing.T) {
	cases := map[string]int{
		"I love pizza. I play piano!":         2,
		"我喜欢比萨。我也会弹钢琴！":                      2,
		"one sentence only":                   1,
		"":                                    0,
	}
	for in, want := range cases {
		got := len(splitSentences(in))
		if got != want {
			t.Errorf("splitSentences(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestNormalize_DefaultsAreSane(t *testing.T) {
	if got := normalizeContentType("nonsense"); got != types.ContentTypeFact {
		t.Errorf("ContentType default wrong: %v", got)
	}
	if got := normalizeType("nonsense"); got != types.MemoryTypeSemantic {
		t.Errorf("MemoryType default wrong: %v", got)
	}
	if got := normalizeSalience("nonsense"); got != types.SalienceNormal {
		t.Errorf("Salience default wrong: %v", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("truncate no-op failed: %q", got)
	}
	if got := truncate("hello world", 5); !strings.HasPrefix(got, "hello") || !strings.HasSuffix(got, "…") {
		t.Errorf("truncate failed: %q", got)
	}
}
