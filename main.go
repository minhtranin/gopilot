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
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	port := flag.String("port", "4141", "port to listen on")
	login := flag.Bool("login", false, "force a fresh GitHub device login (switch account), then exit")
	usage := flag.Bool("usage", false, "print Copilot plan and quota usage, then exit")
	models := flag.Bool("models", false, "list models your Copilot subscription offers, then exit")
	flag.Parse()

	if *login {
		tok, err := deviceLogin()
		if err != nil {
			log.Fatal("github auth: ", err)
		}
		if err := saveGitHubToken(tok); err != nil {
			log.Fatal("save token: ", err)
		}
		if u, err := fetchCopilotUser(tok); err == nil {
			fmt.Printf("logged in as %s (plan: %s)\n", u.Login, u.Plan)
		} else {
			fmt.Println("logged in; could not read Copilot plan:", err)
		}
		return
	}

	githubToken, err := ensureGitHubToken()
	if err != nil {
		log.Fatal("github auth: ", err)
	}

	if *usage {
		u, err := fetchCopilotUser(githubToken)
		if err != nil {
			log.Fatal(err)
		}
		printUsage(os.Stdout, u)
		return
	}

	st := newState(githubToken)

	if *models {
		tok, err := fetchCopilotToken(githubToken)
		if err != nil {
			log.Fatal("copilot token: ", err)
		}
		st.setCopilotToken(tok.Token)
		raw, err := fetchModels(st)
		if err != nil {
			log.Fatal(err)
		}
		list, err := parseModels(raw)
		if err != nil {
			log.Fatal("models: ", err)
		}
		printModels(os.Stdout, list)
		return
	}

	if err := refreshLoop(st); err != nil {
		log.Fatal("copilot token: ", err)
	}
	log.Println("[gopilot] copilot token acquired, refresher running")

	mux := newServer(st)
	log.Printf("[gopilot] listening on :%s\n", *port)
	log.Fatal(http.ListenAndServe(":"+*port, mux))
}
