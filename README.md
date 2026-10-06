# gopilot

Tiny Go proxy. Claude Code talk Anthropic. GitHub Copilot talk OpenAI. gopilot sit in middle, translate both ways. Use your Copilot sub as Claude Code backend.

```
Claude Code / ocr  ──Anthropic──►  gopilot :4142  ──OpenAI──►  api.githubcopilot.com
```

Go rewrite of the JS [`copilot-api`](https://github.com/ericc-ch/copilot-api). Same login token file, so already logged in there = no re-login here.

## Why not just use copilot-api

Four bugs. gopilot fix all:

| Problem | What happen | gopilot fix |
|---|---|---|
| Claude Code inject `x-anthropic-billing-header:` line into system prompt | Copilot-hosted models think it injection, **ignore whole system prompt** (your CLAUDE.md, `--system-prompt-file`) | strip line before Copilot see it |
| Gemini on Copilot = reasoning model | no `reasoning_effort` → burn all `max_tokens` thinking, answer cut at ~14 tokens | force `reasoning_effort: "low"` for `gemini*` |
| Burst of parallel calls (e.g. `ocr` review = 1 call per file, 51 in 10s) | most get HTTP 429 | max 4 in flight + retry with `Retry-After` backoff |
| `claude-sonnet-4-20250514` style ids | Copilot 404 | map to bare `claude-sonnet-4` / `claude-opus-4` |

Also: `gpt-5.6-*` models go through Copilot `/responses` API automatically.

## Need

- Go 1.26+
- GitHub account with **active Copilot subscription**

## Run

```bash
git clone https://github.com/minhtranin/gopilot && cd gopilot
go build -o gopilot .
./gopilot -port 4142
```

First run: no token → prints device code:
```
Open https://github.com/login/device and enter code: ABCD-1234
```
Open link, enter code, approve. Token saved to `~/.local/share/copilot-api/github_token`. Next runs skip login.

Check alive:
```bash
curl localhost:4142/            # gopilot running
curl localhost:4142/v1/models   # models your Copilot sub has
```

## Use with Claude Code

```bash
export ANTHROPIC_BASE_URL=http://localhost:4142
export ANTHROPIC_AUTH_TOKEN=dummy           # gopilot ignore it, Claude Code just want something
export ANTHROPIC_MODEL=gemini-3.8-flash     # any id from /v1/models
export ANTHROPIC_SMALL_FAST_MODEL=gemini-3.8-flash
export DISABLE_NON_ESSENTIAL_MODEL_CALLS=1
export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
claude
```

fish wrapper, so normal `claude` stay untouched:
```fish
function ccc
    set -lx ANTHROPIC_BASE_URL http://localhost:4142
    set -lx ANTHROPIC_AUTH_TOKEN dummy
    set -lx ANTHROPIC_MODEL gemini-3.8-flash
    set -lx ANTHROPIC_SMALL_FAST_MODEL gemini-3.8-flash
    set -lx API_TIMEOUT_MS 3000000
    claude $argv
end
```

## Use with anything Anthropic-shaped

Endpoint `POST /v1/messages`. Stream + non-stream. Tools + vision work:

```bash
curl localhost:4142/v1/messages -H 'content-type: application/json' -d '{
  "model": "gemini-3.8-flash", "max_tokens": 256,
  "messages": [{"role": "user", "content": "say ok"}]
}'
```

## What translate

| Anthropic in | OpenAI out |
|---|---|
| `system` | `role: system` message |
| `tools[].input_schema` | `tools[].function.parameters` |
| `tool_choice: any` | `"required"` |
| `tool_use` / `tool_result` | `tool_calls` / `role: tool` message |
| `image` base64 block | `image_url` data URI + `copilot-vision-request: true` header |

Back: `tool_calls` → `tool_use`, `finish_reason` → `stop_reason`.

## Tests

```bash
go test ./...
```

## ⚠ Read before use

- **No auth on the proxy.** Listens on **all interfaces** (`:4142`). Anyone who reach that port use **your** Copilot. Run on your own machine only, behind firewall. Don't expose to internet.
- **Unofficial.** Pretends to be the VS Code Copilot Chat extension (same headers) because Copilot reject anything else. Not affiliated with GitHub. Using Copilot this way may break GitHub's Terms of Service. Own risk. Use your own subscription, not someone else's.
- Copilot rate limits still apply. Heavy use → 429s → slower, not broken.
- Logs print request bodies (truncated). Don't share your logs.
