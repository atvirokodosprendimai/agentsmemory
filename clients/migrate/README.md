# Migrate a mempalace into agentsmemory

Move your existing local Python **mempalace** into an agentsmemory workspace —
every drawer, diary entry, closet, knowledge-graph fact and explicit tunnel.

The tool here (`mempalace_export.py`) is **read-only**: it imports the `mempalace`
package only to *read* your palace (it never writes to it), serialises everything
as newline-delimited JSON (NDJSON), and streams it to the server's `/import`
endpoint over HTTPS with your project's API key.

The server **re-embeds** every memory with its own model, so the bundle carries
**text only** (no vectors) — it stays small and portable, and your migrated
memories are searched by the same embedder as native writes.

---

## What gets migrated

| Kind | Source in mempalace | Notes |
|---|---|---|
| Drawers | the drawer collection | verbatim — wing, room, source, chunk index, entities, dates preserved |
| Diary | drawers in the diary room | carried as drawers with their agent/topic |
| Closets | the closets collection | the topic/quote pointer index (re-embedded) |
| KG facts | `knowledge_graph.sqlite3` | subject → predicate → object with validity dates |
| Tunnels | explicit tunnels | user-authored cross-wing links only |

**Derived state is rebuilt, not copied.** Hallways and entity/topic tunnels are
regenerated server-side from your drawers after the import, so they are never
sent over the wire.

---

## Prerequisites

- **Python 3** on the machine that has your mempalace.
- The **`mempalace` package importable** there (your existing install). If it
  isn't on `sys.path`, pass `--mempalace-path /path/to/mempalace`.
- Your **server URL** (e.g. `https://memory.example.com`) and a **project API
  key**. Create a project in the dashboard and **Reveal** its key, or copy the
  one printed in the server log on first boot.

You do **not** need Ollama/Qdrant locally — embedding happens on the server.

---

## Quick start

```bash
# 1) (optional) Export to a file and look at it first.
python mempalace_export.py --out palace.ndjson

# 2) Stream it straight into your workspace.
python mempalace_export.py --push \
  --server https://memory.example.com \
  --token sk_live_xxxxxxxx
```

The token can also come from the environment so it never lands in your shell
history:

```bash
export AGENTSMEMORY_TOKEN=sk_live_xxxxxxxx
python mempalace_export.py --push --server https://memory.example.com
```

### Push a bundle exported earlier (e.g. on another machine)

`--file` uploads a pre-exported bundle and needs **no** palace and **no**
`mempalace` package — it is a pure upload:

```bash
python mempalace_export.py --file palace.ndjson --push \
  --server https://memory.example.com --token sk_live_xxxxxxxx
```

---

## What you'll see

The server streams progress back as it files records:

```
  pushing to https://memory.example.com/import ...
  filed 4120/24068  (drawers 4000, closets 100, facts 20, tunnels 0, skipped 0)
  ...
  done.  hallways rebuilt: 318  in 142000ms
```

The import is **idempotent**: drawer ids are recomputed under your tenant, so if
a push is interrupted, just run it again — it upserts what's already there and
finishes the rest rather than duplicating.

---

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--out FILE` | — | Write the NDJSON bundle to a file. |
| `--push` | — | Stream the bundle to `<server>/import`. |
| `--file FILE` | — | Push a previously exported bundle (skips reading a palace). |
| `--server URL` | — | Server base URL (required with `--push`). |
| `--token TOK` | `$AGENTSMEMORY_TOKEN` | Project API key / Bearer (required with `--push`). |
| `--palace DIR` | your configured palace | Palace directory to export. |
| `--kg-db PATH` | `~/.mempalace/knowledge_graph.sqlite3` | Knowledge-graph sqlite to export. |
| `--mempalace-path DIR` | — | Where the `mempalace` package lives, if not already importable. |

Pass `--out` and `--push` together to keep a local copy *and* upload it.

---

## How it works (under the hood)

- `POST /import` sits behind the **same Bearer gate as `/mcp`** — your API key is
  resolved to your workspace before anything is read. No key → `401`.
- The body is streaming **NDJSON**: one JSON object per line, discriminated by a
  `kind` field (`manifest`, `drawer`, `closet`, `kg`, `tunnel`), emitted drawers-
  first so tunnel endpoints exist by the time tunnels are applied.
- The whole migration is metered as **one** request against your monthly quota,
  not one per drawer.
- Records stream through in batches; the server embeds and stores incrementally,
  so even a very large palace never has to fit in memory on either side.

---

## Troubleshooting

- **`could not import the mempalace package`** — run the tool where mempalace is
  installed, or pass `--mempalace-path /path/to/mempalace`.
- **`HTTP 401`** — the token is missing or wrong. Reveal the project's API key in
  the dashboard and pass it as `--token` (or `AGENTSMEMORY_TOKEN`).
- **`HTTP 429` / "monthly request cap reached"** — the project hit its plan's
  monthly request cap; upgrade the plan and re-run (the import resumes idempotently).
- **A few records `skipped`** — records with a blank wing/room/content, or a KG
  fact the server couldn't validate, are skipped so one bad row never aborts the
  migration. The summary line reports the count.
- **Diary entries don't show in `am_diary_read`** — mempalace files diary entries
  in the `daily` room; they import verbatim and are fully **searchable**, but
  `am_diary_read` (which scopes to the `diary` room) won't list them. This is a
  known v1 limitation.
- **Very large palace** — the upload is capped at 256 MiB. That fits hundreds of
  thousands of chunked drawers; if you somehow exceed it, export in pieces with
  `--palace` pointed at separate palaces, or open an issue.
