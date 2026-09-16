// gopilot — Go replacement for the JS copilot-api proxy that sits between
// Claude Code (ANTHROPIC_BASE_URL) and a GitHub Copilot subscription.
//
// Built to fix a real bug the JS version can't: Claude Code's SDK injects
// an "x-anthropic-billing-header: ..." line into the system prompt on
// every request, regardless of provider. Copilot-hosted claude-haiku-4.5
// reads that header-shaped text as a suspicious injection and silently
// discounts the whole system block — including your actual --system-prompt-
// file / CLAUDE.md instructions. See the comment on injectedMarkerRE in
// translate.go for the reproduction. Owning translation means we strip it
// before Copilot ever sees it.
//
// Reuses the GitHub device-token cache at
// ~/.local/share/copilot-api/github_token (same path the JS tool uses) —
// no re-auth needed if that tool has already been logged in.
package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	port := flag.String("port", "4141", "port to listen on")
	flag.Parse()

	githubToken, err := ensureGitHubToken()
	if err != nil {
		log.Fatal("github auth: ", err)
	}

	st := newState(githubToken)
	if err := refreshLoop(st); err != nil {
		log.Fatal("copilot token: ", err)
	}
	log.Println("[gopilot] copilot token acquired, refresher running")

	mux := newServer(st)
	log.Printf("[gopilot] listening on :%s\n", *port)
	log.Fatal(http.ListenAndServe(":"+*port, mux))
}
