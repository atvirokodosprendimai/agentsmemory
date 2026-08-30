-- +goose Up
-- rerank_skip_reason records WHY a cross-encoder did not order this page.
--
-- `reranked` cannot carry it: it is 0 alike for no reranker configured, an empty
-- pool, weight<=0, a wrong score count, and a timeout — so am_recall_stats
-- reported the same thing for a deliberately disabled cross-encoder and for one
-- failing on every query, and nothing could answer what fraction of recalls
-- served a degraded ranking.
--
-- NULLABLE and additive on purpose. NULL means "written before this column
-- existed", which is NOT the same as "nothing was skipped" — the aggregate
-- excludes NULL rather than counting it as healthy. An empty string means
-- reranking ran.
ALTER TABLE search_events ADD COLUMN rerank_skip_reason TEXT;

-- +goose Down
ALTER TABLE search_events DROP COLUMN rerank_skip_reason;
