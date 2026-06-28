# Ranking parity fixtures (frozen mempalace regression)

`agentsmemory`'s `internal/palace/rank.go` is a **faithful Go port** of the frozen
Python mempalace's ranking math. The project contract is *"the mempalace part stays
the same as frozen"* — so a silent numeric or ordering drift between the two is a
correctness bug. This directory makes that contract a test.

## How it works

```
gen_golden.py  ──run──►  *.json (frozen's output)  ──read──►  parity_test.go (asserts Go == frozen)
```

1. **`gen_golden.py`** loads the *real* frozen functions from
   `/Users/mind/.claude/mempalace-frozen/mempalace/searcher.py` and runs them over a
   fixed corpus. It stubs the `mempalace.backends` / `mempalace.palace` imports (which
   would otherwise pull chromadb/qdrant/ollama) because the four ranking functions use
   only `math`/`re` — so the executed bodies are byte-for-byte frozen, not a copy.
2. The output is written here as golden JSON.
3. **`parity_test.go`** (package `palace`) feeds the Go port the *identical* inputs
   from the JSON and asserts the result matches the golden.

Because the JSON is the reference, `go test` needs **no Python and no ollama** — it is
deterministic and CI-clean. The frozen package never changes, so the goldens are
stable; regenerate only if the frozen path or the corpus in `gen_golden.py` changes.

## Surfaces covered (ranking math only)

| Fixture | Go function (`rank.go`) | Frozen function (`searcher.py`) |
|---|---|---|
| `tokenize.json`   | `tokenize`            | `_tokenize` |
| `bm25.json`       | `bm25Scores`          | `_bm25_scores` |
| `similarity.json` | `vecSimFromDistance`  | `_distance_to_similarity` (cosine) |
| `hybrid.json`     | `rankHybrid`          | `_hybrid_rank` (order + fused score) |

**Out of scope: embeddings.** ollama `bge-m3` vectors are model-dependent and not
byte-reproducible across the Python/Go boundary, so the suite only covers the math
*downstream of a fixed distance*. The cosine distance is an input to these fixtures,
never a computed value.

## Regenerate

```sh
go generate ./internal/palace        # runs gen_golden.py via the //go:generate directive
# or directly:
python3 internal/palace/testdata/parity/gen_golden.py
```

Requires only a stock `python3` (the heavy mempalace deps are stubbed). After
regenerating, run `go test ./internal/palace -run Parity` — a diff in the goldens that
makes the Go test fail means the port drifted from frozen (or frozen itself changed).
