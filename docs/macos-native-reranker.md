# macOS: run the reranker natively, or every search waits ten seconds

**Who this is for.** Anyone running the full-quality stack
(`docker-compose.full.yml`) on a Mac. If your Claude Code session pauses for
about ten seconds at start and again on every prompt, this is why, and this is
the fix.

## What is wrong

The full stack ships the cross-encoder (`bge-reranker-v2-m3`, llama.cpp) as a
container. Docker on macOS has no GPU: the container runs on the CPU cores it
is capped to (`AGENTSMEMORY_RERANK_CPUS`, 4 by default). Measured 2026-09-05 on
an Apple Silicon laptop, from inside the server container, ten documents each:

| chars per document | container reranker (CPU) | native llama.cpp (Metal) |
|---|---|---|
| 300 | 3.7 s | — |
| 800 | 13.6 s | — |
| 1500 | 30.0 s | 2.3 s |

The server sends the reranker up to 1600 characters per document and a pool of
10, against a budget of `RERANK_TIMEOUT=10s`. On the CPU that can never finish:
the rerank times out, search falls open to the fused order, and you have paid
ten seconds for nothing. Both agentsmemory hooks that recall on `SessionStart`
and `UserPromptSubmit` run one such search, which is the pause you see. The
condition is visible in the span tree the server logs:

```
am.search.rerank  10076ms  failed_open reason=timeout
POST /v1/rerank   10018ms
```

## The fix: llama.cpp on the host, with Metal

Homebrew's llama.cpp is built with Metal. The same model, the same endpoint,
thirteen times faster on the measurement above.

```bash
brew install llama.cpp
```

Run it once by hand to pull the model and check it answers:

```bash
llama-server --reranking -hf gpustack/bge-reranker-v2-m3-GGUF:Q4_K_M \
  -c 4096 -b 4096 -ub 4096 --host 127.0.0.1 --port 8081
curl -sf http://127.0.0.1:8081/health
```

`-b 4096 -ub 4096` matters: the default physical batch is 512 tokens, and a
1600-character document is about 1100 tokens, so without it the server answers
`input (1138 tokens) is too large to process` with HTTP 500 and the palace falls
open exactly as before, only faster.

Keep it alive across logins with a LaunchAgent. Save this as
`~/Library/LaunchAgents/agentsmemory.reranker.plist`, with `HOME` spelled out:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>agentsmemory.reranker</string>
  <key>ProgramArguments</key><array>
    <string>/opt/homebrew/bin/llama-server</string>
    <string>--reranking</string>
    <string>-hf</string><string>gpustack/bge-reranker-v2-m3-GGUF:Q4_K_M</string>
    <string>-c</string><string>4096</string>
    <string>-b</string><string>4096</string>
    <string>-ub</string><string>4096</string>
    <string>--host</string><string>127.0.0.1</string>
    <string>--port</string><string>8081</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/Users/YOU/Library/Logs/agentsmemory/reranker.log</string>
  <key>StandardErrorPath</key><string>/Users/YOU/Library/Logs/agentsmemory/reranker.log</string>
</dict></plist>
```

```bash
mkdir -p ~/Library/Logs/agentsmemory
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/agentsmemory.reranker.plist
until curl -sf http://127.0.0.1:8081/health >/dev/null; do sleep 2; done
```

## Point the stack at it

`RERANK_URL` in `docker-compose.full.yml` is interpolated from your shell or
from a `.env` file beside the compose files; it is NOT read from `.env.docker`,
because the overlay's `environment:` entry wins over `env_file:`. So:

```bash
echo 'RERANK_URL=http://host.docker.internal:8081/v1' >> .env
docker compose -f docker-compose.yml -f docker-compose.full.yml up -d
```

Contributors who redeploy with `scripts/redeploy.sh` from a clone: the clone has
no `.env`, so copy it in beside `.env.docker` (AGENTS.md's procedure now does),
or export `RERANK_URL` in the shell that runs the script.

The container reranker still starts, because the server's `depends_on` waits
for it to be healthy; it idles once nothing talks to it. Cap it with
`AGENTSMEMORY_RERANK_CPUS=1` if the idle load bothers you.

## Check it worked

One search through the endpoint the hooks use, then the span tree:

```bash
AGENTSMEMORY_MCP_URL=http://localhost:8080/mcp aiagentmemory mcp search \
  --arg query="anything" --arg limit=5 >/dev/null
docker logs --since 1m agentsmemory-agentsmemory-1 2>&1 | grep -E 'am.search.rerank|/v1/rerank'
```

You want `am.search.rerank … ran` in low single-digit seconds, not
`failed_open reason=timeout`. Then time a session start:

```bash
printf '{"session_id":"probe","transcript_path":"/dev/null","cwd":"%s","hook_event_name":"SessionStart","source":"startup"}' "$PWD" \
  | time bash ~/.claude/agentsmemory-recall-hook.sh >/dev/null
```

## If you would rather not run anything on the host

Set `RERANK_URL=` (empty) in `.env` to turn the cross-encoder off, or
`RERANK_TIMEOUT=2s` to cap the wait. Either drops the hook to two or three
seconds (that is the embedder) at the cost of the rerank, which on the CPU you
were not getting anyway.
