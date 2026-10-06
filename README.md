# gopilot

Go proxy. Copilot sub → Claude Code backend.

```
Claude Code ──Anthropic──► gopilot :4142 ──OpenAI──► api.githubcopilot.com
```

## Need

Go 1.26+. GitHub account with Copilot.

## Run

```bash
git clone https://github.com/minhtranin/gopilot && cd gopilot
go build -o gopilot .
./gopilot -port 4142      # first run: open printed link, enter code. Token saved.
```

## Commands

| | |
|---|---|
| `./gopilot -port 4142` | run proxy |
| `./gopilot -models` | list models your sub has |
| `./gopilot -usage` | plan + quota left |
| `./gopilot -login` | re-login / switch account |

Watch `premium_interactions` in `-usage`. That one run out.

## Claude Code

```bash
export ANTHROPIC_BASE_URL=http://localhost:4142
export ANTHROPIC_AUTH_TOKEN=dummy               # any value
export ANTHROPIC_MODEL=gemini-3.8-flash         # pick from -models
export ANTHROPIC_SMALL_FAST_MODEL=gemini-3.8-flash
claude
```

## Fix vs copilot-api

- Strip `x-anthropic-billing-header` line. Else Copilot ignore whole system prompt.
- Gemini: force `reasoning_effort: low`. Else answer cut after ~14 tokens.
- Max 4 req in flight + retry on 429. Parallel burst no die.
- `claude-sonnet-4-2025…` → `claude-sonnet-4`. No 404.
- `gpt-5.6-*` → `/responses` auto.

Tools, vision, streaming: work.

## ⚠

- **No auth.** Listen all interfaces. Anyone reach port = use your Copilot. Local only. Firewall.
- **Unofficial.** Fake VS Code Copilot headers. May break GitHub ToS. Own risk, own sub.
- Logs show request bodies. No share logs.
