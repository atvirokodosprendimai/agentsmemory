# Changelog

- 2026-06-26 `95aad7b` feat: scaffold multi-tenant MCP memory server (Qdrant + Ollama)
- 2026-06-26 `02e0f08` docs: add comprehensive project README
- 2026-06-26 `38c0f83` feat: dashboard (register/login/free projects) + usage metering
- 2026-06-26 `56e9f87` feat: OAuth 2.1 MCP auth (merged AS + credential validation) + .env
- 2026-06-26 `2d0b753` harden: fold in Codex OAuth security review findings
- 2026-06-26 `4b2e051` ci: Dockerfile + release workflow (build only on vX.X.X tags)
- 2026-06-26 `b8f6f75` feat(store): SQLite source-of-truth + Qdrant search index seam
- 2026-06-26 `24fc494` feat(server): select vector backend via config/flag/env
- 2026-06-26 `ae9b659` feat(server): bind --addr and --db to env
- 2026-06-26 `1965460` feat(palace): drawer domain — table, repo, chunking, service
- 2026-06-26 `b665921` feat(mcpserver): wire 12 core memory-loop MCP tools
- 2026-06-26 `f7538a3` fix(palace): snake_case json tags on WingStat/RoomStat
- 2026-06-26 `6524e5d` docs: README reflects core memory loop (14/37 MCP tools)
- 2026-06-26 `676b38f` fix(palace): fold Codex review findings
- 2026-06-26 `c323d33` feat(server): APP_DEBUG flag — request + SQL logging
- 2026-06-28 `5baed29` feat(palace): diary tools — diary_write / diary_read (16/37)
- 2026-06-28 `e78e0df` docs: changelog — diary tools (16/37)
- 2026-06-28 `d38f65e` feat(palace): hybrid search — vector candidates re-ranked by vector+BM25
