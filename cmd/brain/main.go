// brain — a standalone memory-backed LLM gateway.
// Subcommands:
//   brain serve              start the gateway server
//   brain gateway-config     emit config for the chosen CLI
//   brain migrate            run V1→V2 memory data migration
package main

import (
	"os"
	"strings"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printUsage()
		return
	}
	cmd, remaining := args[0], args[1:]

	switch cmd {
	case "serve":
		runServe()
	case "gateway-config", "gw":
		if len(remaining) < 1 {
			printUsage()
			os.Exit(2)
		}
		cli := remaining[0]
		baseURL := flag(remaining, "--base-url", "http://localhost:8090")
		token := flag(remaining, "--token", os.Getenv("BRAIN_TOKEN"))
		model := flag(remaining, "--model", "deepseek-chat")
		runGatewayConfig(cli, baseURL, token, model)
	case "migrate":
		dryRun := hasFlag(remaining, "--dry-run")
		sources := flag(remaining, "--sources", "")
		runMigrate(dryRun, sources)
	case "help", "-h", "--help":
		printUsage()
	default:
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	os.Stderr.WriteString(strings.TrimSpace(`
brain — memory-backed LLM gateway

Usage:
  brain serve                                       start the gateway
  brain gateway-config <cli> [--base-url URL] [--token TOK] [--model M]
  brain migrate [--dry-run] [--sources core_memories,extracted,user_profiles]

Supported CLIs (for gateway-config):
  opencode  codex  aider  claude-code  continue  litellm

Env (for serve + migrate):
  DATABASE_URL  EMBEDDING_BASE_URL  EMBEDDING_API_KEY  EMBEDDING_MODEL
  UPSTREAM_LLM_BASE_URL  UPSTREAM_LLM_API_KEY  UPSTREAM_LLM_MODEL
  BRAIN_PORT(8090)  BRAIN_USER_ID  BRAIN_TOKEN
`) + "\n")
}

func flag(args []string, name, def string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == name { return args[i+1] }
	}
	return def
}

func hasFlag(args []string, name string) bool {
	for _, a := range args { if a == name { return true } }
	return false
}
