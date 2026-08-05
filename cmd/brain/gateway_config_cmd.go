package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func runGatewayConfig(cliName, baseURL, token, model string) {
	if baseURL == "" { baseURL = "http://localhost:8090" }
	if model == ""   { model = "deepseek-chat" }
	if token == ""   { token = "<YOUR_BRAIN_TOKEN>" }
	baseURL = strings.TrimRight(baseURL, "/")
	fmt.Fprintln(os.Stderr, runGatewayConfigEmit(cliName, baseURL, token, model))
}

func runGatewayConfigEmit(cli, baseURL, token, model string) string {
	switch strings.ToLower(cli) {
	case "opencode":
		obj := map[string]any{
			"$schema": "https://opencode.ai/config.json",
			"provider": map[string]any{
				"brain": map[string]any{
					"npm": "@ai-sdk/openai-compatible", "name": "Brain",
					"options": map[string]any{"baseURL": baseURL + "/v1"},
					"models":  map[string]any{model: map[string]any{"name": model}},
					"api_key_env": "BRAIN_TOKEN",
				},
			},
			"model": "brain/" + model,
		}
		b, _ := json.MarshalIndent(obj, "", "  ")
		return "# opencode.json\n# export BRAIN_TOKEN=" + token + "\n" + string(b)

	case "codex":
		return fmt.Sprintf("# Codex CLI\n"+
			"export OPENAI_BASE_URL=%s/v1\n"+
			"export OPENAI_API_KEY=%s\n"+
			"# Then: codex --model %s", baseURL, token, model)

	case "aider":
		return fmt.Sprintf("# Aider\n"+
			"export OPENAI_API_BASE=%s/v1\n"+
			"export OPENAI_API_KEY=%s\n"+
			"# Run: aider --model openai/%s", baseURL, token, model)

	case "claude-code":
		return fmt.Sprintf("# Claude Code (Anthropic protocol)\n"+
			"export ANTHROPIC_BASE_URL=%s\n"+
			"export ANTHROPIC_API_KEY=%s\n", baseURL, token)

	case "continue":
		obj := map[string]any{
			"models": []map[string]any{{"title": "Brain", "provider": "openai",
				"model": model, "apiBase": baseURL + "/v1", "apiKey": token}},
		}
		b, _ := json.MarshalIndent(obj, "", "  ")
		return "# Continue (~/.continue/config.json)\n" + string(b)

	case "litellm":
		return fmt.Sprintf("# LiteLLM proxy\nmodel_list:\n  - model_name: brain\n"+
			"    litellm_params:\n      model: openai/%s\n      api_base: %s/v1\n      api_key: %s", model, baseURL, token)

	default:
		return fmt.Sprintf("unknown CLI %q — supported: opencode, codex, aider, claude-code, continue, litellm", cli)
	}
}
