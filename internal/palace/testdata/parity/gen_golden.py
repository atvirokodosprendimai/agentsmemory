#!/usr/bin/env python3
"""gen_golden.py — capture frozen-mempalace ranking output as golden fixtures.

agentsmemory's ``internal/palace/rank.go`` is a faithful Go port of the frozen
Python searcher's ranking math. "Same data -> same results" is the project's
correctness contract, so this harness runs the *real* frozen functions over a
fixed corpus and writes their output to JSON. ``parity_test.go`` then loads that
JSON and asserts the Go port produces the same thing. The frozen package never
changes (it is frozen), so these goldens are a stable reference — regenerate only
if the frozen path or the corpus below changes.

Why load the frozen functions instead of re-implementing them: a re-implementation
would test the harness against itself, not against frozen. We execute the actual
``searcher.py`` source. Its module top imports ``.backends`` and ``.palace`` (which
would pull chromadb/qdrant/ollama), but the four ranking functions we call use only
``math`` and ``re`` — so we register lightweight stub submodules for those two
imports and let the genuine ``searcher.py`` body run untouched.

Scope: ranking math ONLY (tokenize, BM25, distance->similarity, hybrid order),
matching the regression suite's agreed scope. Embeddings are excluded on purpose —
ollama bge-m3 vectors are model-dependent and not byte-reproducible across the
Python/Go boundary, so only the *downstream* math (which takes a fixed distance as
input) is comparable.

Run:  python3 internal/palace/testdata/parity/gen_golden.py
"""

import importlib.util
import json
import sys
import types
from pathlib import Path

# Frozen mempalace package — the reference implementation we are porting from.
FROZEN = Path("/Users/mind/.claude/mempalace-frozen/mempalace")
HERE = Path(__file__).resolve().parent


def load_frozen_searcher():
    """Execute the real frozen searcher.py with its heavy imports stubbed.

    searcher.py does ``from .backends import ...`` and ``from .palace import ...``
    at module load. Those submodules pull optional vector-store / embedding deps
    that the four ranking functions never touch at runtime. We register minimal
    stub modules under the ``mempalace`` package name so the relative imports
    resolve, then exec the genuine searcher.py source — so ``_tokenize`` et al. are
    byte-for-byte the frozen implementation, not a copy.
    """
    if not (FROZEN / "searcher.py").is_file():
        sys.exit(f"frozen searcher not found at {FROZEN}/searcher.py")

    pkg = types.ModuleType("mempalace")
    pkg.__path__ = [str(FROZEN)]
    sys.modules["mempalace"] = pkg

    backends = types.ModuleType("mempalace.backends")
    for name in (
        "BackendError",
        "BackendMismatchError",
        "CollectionNotInitializedError",
        "PalaceNotFoundError",
        "UnsupportedCapabilityError",
    ):
        setattr(backends, name, type(name, (Exception,), {}))
    sys.modules["mempalace.backends"] = backends

    palace = types.ModuleType("mempalace.palace")
    for name in (
        "_open_collection_or_explain",
        "get_closets_collection",
        "get_collection",
        "resolve_backend_name",
    ):
        setattr(palace, name, lambda *a, **k: None)
    sys.modules["mempalace.palace"] = palace

    spec = importlib.util.spec_from_file_location("mempalace.searcher", FROZEN / "searcher.py")
    mod = importlib.util.module_from_spec(spec)
    sys.modules["mempalace.searcher"] = mod
    spec.loader.exec_module(mod)
    return mod


# --- Shared corpora --------------------------------------------------------
# Each case is a plain dict; the same inputs are echoed into the JSON so the Go
# test feeds the Go port exactly what the frozen function saw.

# tokenize: stress the Python \w+re.UNICODE vs Go [\p{L}\p{N}_] boundary and the
# text.lower() vs strings.ToLower boundary — accents, ligatures, eszett, CJK,
# digits, underscores, length<2 drops, punctuation, emoji, whitespace.
TOKENIZE_INPUTS = [
    ("ascii_basic", "The LRU-cache, evicts! 2x x"),
    ("empty", ""),
    ("single_chars_dropped", "a b cd e fg"),
    ("digits_underscore", "foo_bar 123 a1b2 __init__ v2"),
    ("accents", "Café NAÏVE résumé Über"),
    ("ligature_eszett", "ﬁle ﬀ straße GROSS"),
    ("cjk", "中文 测试 데이터 こんにちは"),
    ("punctuation_heavy", "a.b.c, x---y; (z) e2e"),
    ("model_names", "GPT-4 BGE-m3 2800/400 v1.0.2"),
    ("emoji_symbols", "hello 👋 world ™ café®"),
    ("whitespace", "line1\nline2\ttab  spaced"),
    ("mixed_case", "MixedCASE camelCase snake_case"),
]

# bm25: presence/absence, tf saturation, length normalization, idf smoothing
# (a term in every doc must stay >= 0), empty query / corpus, text-less doc, and
# a multi-term query with partial matches.
BM25_CASES = [
    ("presence_beats_absence", "lru cache eviction",
     ["the cache uses an lru eviction policy", "completely unrelated text about the weather"]),
    ("repeated_query_terms_dedup", "cache cache cache",
     ["a cache about a cache", "no match here"]),
    ("tf_saturation", "cache",
     ["cache cache cache cache cache", "cache once only here"]),
    ("length_normalization", "cache",
     ["cache", "cache " + "filler word " * 40]),
    ("term_in_every_doc", "cache",
     ["cache one", "cache two", "cache three"]),
    ("empty_query", "", ["anything at all", "more text"]),
    ("empty_corpus", "cache", []),
    ("textless_doc", "cache", ["cache hit", "!!! ??? ..."]),
    ("multi_term_partial", "lru cache eviction policy",
     ["lru eviction", "cache policy", "the weather is nice", "lru cache eviction policy exact"]),
]

# distance->similarity (cosine only — the sole metric agentsmemory stores use):
# d in [0,2], 0=identical; >1 yields <0 clamped to 0; boundaries around 1.0.
SIMILARITY_DISTANCES = [0.0, 0.25, 0.5, 0.75, 0.999, 1.0, 1.0001, 1.5, 2.0, 2.5]

# hybrid: lexical-promotes-over-vector, no-lexical vector fallback, an exact tie
# (no lexical signal + equal distance -> stable order preserved in both Python and
# Go), a mixed 3-way, and a single candidate. Tie inputs deliberately have zero
# BM25 so the fused scores are bit-exactly equal — avoiding float noise that could
# flip an accidental near-tie differently across the two languages.
HYBRID_CASES = [
    ("lexical_promotes_over_vector", "lru cache eviction",
     ["the cache uses an lru eviction policy", "a quiet meadow at dawn"], [0.5, 0.1]),
    ("no_lexical_vector_fallback", "zzz qqq",
     ["alpha text", "beta text", "gamma text"], [0.3, 0.1, 0.5]),
    ("exact_tie_stable_order", "zzz qqq",
     ["alpha note", "beta note"], [0.5, 0.5]),
    ("mixed_three_way", "lru cache",
     ["lru cache eviction policy", "a quiet meadow", "cache notes here"], [0.6, 0.05, 0.4]),
    ("single_candidate", "cache", ["the cache line"], [0.2]),
]


def build_tokenize(s):
    return [{"name": name, "input": text, "want": s._tokenize(text)}
            for name, text in TOKENIZE_INPUTS]


def build_bm25(s):
    return [{"name": name, "query": q, "docs": docs, "want": s._bm25_scores(q, docs)}
            for name, q, docs in BM25_CASES]


def build_similarity(s):
    return [{"name": f"cosine_d_{d}", "distance": d, "want": s._distance_to_similarity(d, "cosine")}
            for d in SIMILARITY_DISTANCES]


def build_hybrid(s):
    cases = []
    for name, query, docs, distances in HYBRID_CASES:
        # Order: run the genuine frozen re-rank with each candidate tagged by its
        # original index, then read the post-sort order back out.
        results = [{"_idx": i, "text": d, "distance": dist}
                   for i, (d, dist) in enumerate(zip(docs, distances))]
        s._hybrid_rank(results, query)
        want_order = [r["_idx"] for r in results]

        # Fused score per ORIGINAL index, recomputed with frozen helpers so the Go
        # test can compare component-by-component (the re-rank discards the key).
        bm25_raw = s._bm25_scores(query, docs)
        max_bm25 = max(bm25_raw) if bm25_raw else 0.0
        bm25_norm = [x / max_bm25 if max_bm25 > 0 else 0.0 for x in bm25_raw]
        want_fused = [0.6 * s._distance_to_similarity(dist, "cosine") + 0.4 * nm
                      for dist, nm in zip(distances, bm25_norm)]

        cases.append({
            "name": name, "query": query, "docs": docs, "distances": distances,
            "want_order": want_order, "want_fused": want_fused,
        })
    return cases


def write_json(filename, cases):
    """Write one fixture file with a provenance header + its cases."""
    payload = {
        "_generated_by": "internal/palace/testdata/parity/gen_golden.py",
        "_frozen_reference": str(FROZEN),
        "_note": "golden output of the frozen Python ranking funcs; regenerate via gen_golden.py",
        "cases": cases,
    }
    out = HERE / filename
    # ensure_ascii=False keeps the unicode corpus human-readable in the fixture.
    out.write_text(json.dumps(payload, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"wrote {out.relative_to(HERE.parent.parent.parent.parent)} ({len(cases)} cases)")


def main():
    s = load_frozen_searcher()
    write_json("tokenize.json", build_tokenize(s))
    write_json("bm25.json", build_bm25(s))
    write_json("similarity.json", build_similarity(s))
    write_json("hybrid.json", build_hybrid(s))


if __name__ == "__main__":
    main()
