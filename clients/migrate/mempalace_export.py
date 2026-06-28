#!/usr/bin/env python3
"""mempalace_export — migrate a local mempalace into the agentsmemory SaaS.

This is a READ-ONLY exporter and pusher. It imports the frozen ``mempalace``
package purely to read your palace (it never writes to it), serialises every
drawer, diary entry, closet, knowledge-graph fact and explicit tunnel as
newline-delimited JSON (NDJSON), and — with ``--push`` — streams that bundle to
your project's ``/import`` endpoint over HTTPS using your API token.

The server re-embeds every drawer with its own model, so the bundle carries TEXT
only (no vectors): it stays small and portable.

Two modes (combinable):

    # 1) Write a bundle you can inspect first:
    python mempalace_export.py --out palace.ndjson

    # 2) Stream straight to the SaaS:
    python mempalace_export.py --push \\
        --server https://memory.example.com --token sk_live_xxx

Run it where the ``mempalace`` package is importable (your existing install), or
pass ``--mempalace-path /path/to/mempalace-frozen`` to point at it.
"""

import argparse
import http.client
import json
import os
import re
import sqlite3
import sys
from urllib.parse import urlparse

# A bare YYYY-MM-DD prefix is all the SaaS accepts for KG validity bounds (its
# temporal columns are compared as text, so it allows only a calendar date or a
# canonical UTC datetime). The frozen palace stamps naive microsecond datetimes,
# which would be rejected, so we coarsen every temporal to its date — the day
# granularity that KG validity windows are reasoned about in anyway.
_DATE_PREFIX = re.compile(r"^(\d{4}-\d{2}-\d{2})")


def _date_only(value):
    """Coarsen a temporal value to YYYY-MM-DD, or "" when absent/unparseable."""
    if not value:
        return ""
    m = _DATE_PREFIX.match(str(value).strip())
    return m.group(1) if m else ""


def _as_entities(value):
    """Normalise the stored entities field (list, or ';'/',' joined str) to a list."""
    if isinstance(value, list):
        return [str(e) for e in value if str(e).strip()]
    if isinstance(value, str):
        parts = re.split(r"[;,]", value)
        return [p.strip() for p in parts if p.strip()]
    return []


def _open_collection(get_collection, palace_path, name=None):
    """Open a palace collection, retrying without the embedder-identity check.

    A pure metadata/document read does not need the embedder, but get_collection
    enforces model identity by default; the skip lets export work even when the
    recorded model differs from what is installed (or Ollama is down).
    """
    kwargs = {"create": False}
    if name:
        kwargs["collection_name"] = name
    try:
        return get_collection(palace_path, **kwargs)
    except TypeError:
        # Older signature without collection_name kw — fall back to positional.
        return get_collection(palace_path, create=False)
    except Exception:
        try:
            return get_collection(palace_path, _skip_identity_check=True, **kwargs)
        except Exception:
            return None


def _iter_collection(col, batch=1000):
    """Yield (id, document, metadata) for every record, paginated to bound memory."""
    total = col.count()
    offset = 0
    while offset < total:
        page = col.get(limit=batch, offset=offset, include=["documents", "metadatas"])
        ids = page.get("ids") or []
        if not ids:
            break
        docs = page.get("documents") or []
        metas = page.get("metadatas") or []
        for i, rec_id in enumerate(ids):
            yield rec_id, docs[i], (metas[i] or {})
        offset += len(ids)


def iter_drawers(get_collection, palace_path):
    """Yield drawer records (diary entries included — they are just drawers with
    a room and an agent/topic)."""
    col = _open_collection(get_collection, palace_path)
    if col is None:
        print("  warning: could not open the drawer collection — skipping drawers", file=sys.stderr)
        return
    for _id, doc, meta in _iter_collection(col):
        if not doc:
            continue
        yield {
            "kind": "drawer",
            "wing": meta.get("wing", ""),
            "room": meta.get("room", "general"),
            "source_file": meta.get("source_file", ""),
            "chunk_index": int(meta.get("chunk_index", 0) or 0),
            "content": doc,
            "entities": _as_entities(meta.get("entities")),
            "filed_at": meta.get("filed_at", ""),
            "content_date": meta.get("content_date", ""),
            # The frozen palace records the author as ``added_by``; a diary entry
            # also carries ``agent``/``topic``. Prefer the explicit diary keys.
            "agent": meta.get("agent", meta.get("added_by", "")),
            "topic": meta.get("topic", ""),
        }


def iter_closets(get_closets_collection, palace_path):
    """Yield closet pointer-index records."""
    col = _open_collection(get_closets_collection, palace_path)
    if col is None:
        return
    for _id, doc, meta in _iter_collection(col):
        if not doc:
            continue
        yield {
            "kind": "closet",
            "wing": meta.get("wing", ""),
            "room": meta.get("room", "general"),
            "source_file": meta.get("source_file", ""),
            "document": doc,
            "entities": _as_entities(meta.get("entities")),
            "filed_at": meta.get("filed_at", ""),
        }


def iter_kg(kg_db):
    """Yield knowledge-graph facts with subject/object resolved to display names.

    Reads the KG sqlite directly (read-only). subject/object are entity ids in
    the triples table; we join entities to emit human names, which the SaaS
    re-normalises on its side.
    """
    if not kg_db or not os.path.isfile(kg_db):
        return
    uri = "file:%s?mode=ro" % kg_db
    conn = sqlite3.connect(uri, uri=True)
    try:
        conn.row_factory = sqlite3.Row
        rows = conn.execute(
            """
            SELECT s.name AS subject, t.predicate AS predicate, o.name AS object,
                   t.valid_from AS valid_from, t.valid_to AS valid_to,
                   t.source_file AS source_file
            FROM triples t
            JOIN entities s ON s.id = t.subject
            JOIN entities o ON o.id = t.object
            """
        ).fetchall()
    finally:
        conn.close()
    for r in rows:
        if not r["subject"] or not r["predicate"] or not r["object"]:
            continue
        yield {
            "kind": "kg",
            "subject": r["subject"],
            "predicate": r["predicate"],
            "object": r["object"],
            "valid_from": _date_only(r["valid_from"]),
            "valid_to": _date_only(r["valid_to"]),
            "source_file": r["source_file"] or "",
        }


def iter_tunnels(list_tunnels):
    """Yield explicit (user-authored) cross-wing tunnels. Entity/topic tunnels are
    derived state the SaaS regenerates after import, so they are not exported."""
    try:
        tunnels = list_tunnels()
    except Exception:
        return
    for t in tunnels or []:
        if t.get("kind", "explicit") != "explicit":
            continue
        src = t.get("source") or {}
        tgt = t.get("target") or {}
        if not src.get("wing") or not tgt.get("wing"):
            continue
        yield {
            "kind": "tunnel",
            "source_wing": src.get("wing", ""),
            "source_room": src.get("room", ""),
            "target_wing": tgt.get("wing", ""),
            "target_room": tgt.get("room", ""),
            "label": t.get("label", ""),
        }


def _counts(get_collection, get_closets_collection, list_tunnels, palace_path, kg_db):
    """Cheap up-front totals for the manifest, so the importer can show a real
    progress bar. Each count is best-effort and defaults to 0."""
    total = 0
    try:
        col = _open_collection(get_collection, palace_path)
        total += col.count() if col else 0
    except Exception:
        pass
    try:
        cc = _open_collection(get_closets_collection, palace_path)
        total += cc.count() if cc else 0
    except Exception:
        pass
    if kg_db and os.path.isfile(kg_db):
        try:
            conn = sqlite3.connect("file:%s?mode=ro" % kg_db, uri=True)
            total += conn.execute("SELECT COUNT(*) FROM triples").fetchone()[0]
            conn.close()
        except Exception:
            pass
    try:
        total += sum(1 for t in (list_tunnels() or []) if t.get("kind", "explicit") == "explicit")
    except Exception:
        pass
    return total


def records(mp, palace_path, kg_db):
    """The full export stream: a manifest line, then every record kind in the
    order the importer needs (drawers first so tunnel endpoints exist)."""
    total = _counts(mp.get_collection, mp.get_closets_collection, mp.list_tunnels, palace_path, kg_db)
    yield {"kind": "manifest", "total": total, "source": "mempalace"}
    yield from iter_drawers(mp.get_collection, palace_path)
    yield from iter_closets(mp.get_closets_collection, palace_path)
    yield from iter_kg(kg_db)
    yield from iter_tunnels(mp.list_tunnels)


class _Mempalace:
    """Thin holder for the frozen package's read functions, so the rest of the
    module does not care where they were imported from."""

    def __init__(self):
        from mempalace.palace import get_collection, get_closets_collection
        from mempalace.palace_graph import list_tunnels

        self.get_collection = get_collection
        self.get_closets_collection = get_closets_collection
        self.list_tunnels = list_tunnels


def _default_palace_path():
    """The configured palace path, or the conventional ~/.mempalace fallback."""
    try:
        from mempalace.config import MempalaceConfig

        return os.path.abspath(os.path.expanduser(MempalaceConfig().palace_path))
    except Exception:
        return os.path.expanduser("~/.mempalace")


def _default_kg_db():
    try:
        from mempalace.knowledge_graph import DEFAULT_KG_PATH

        return DEFAULT_KG_PATH
    except Exception:
        return os.path.expanduser("~/.mempalace/knowledge_graph.sqlite3")


def write_file(stream, path):
    """Write the NDJSON bundle to a file, returning how many records were written."""
    n = 0
    with open(path, "w", encoding="utf-8") as f:
        for rec in stream:
            f.write(json.dumps(rec, ensure_ascii=False))
            f.write("\n")
            n += 1
    return n


def _byte_lines(path_or_stream):
    """Yield NDJSON byte lines from a file path or an in-memory record stream."""
    if isinstance(path_or_stream, str):
        with open(path_or_stream, "rb") as f:
            for line in f:
                yield line
    else:
        for rec in path_or_stream:
            yield (json.dumps(rec, ensure_ascii=False) + "\n").encode("utf-8")


def push(source, server, token, insecure=False):
    """Stream the NDJSON bundle to <server>/import with a Bearer token and print
    the server's progress lines as they arrive."""
    u = urlparse(server.rstrip("/") + "/import")
    if u.scheme == "https":
        conn = http.client.HTTPSConnection(u.hostname, u.port or 443)
    else:
        conn = http.client.HTTPConnection(u.hostname, u.port or 80)
    headers = {
        "Authorization": "Bearer " + token,
        "Content-Type": "application/x-ndjson",
    }
    # Passing an iterable body with no Content-Length makes http.client stream it
    # with chunked transfer-encoding — so a 30k-drawer palace never has to be held
    # in memory at once.
    conn.request("POST", u.path, body=_byte_lines(source), headers=headers)
    resp = conn.getresponse()
    if resp.status >= 400:
        body = resp.read().decode("utf-8", "replace").strip()
        raise SystemExit("  push failed: HTTP %d — %s" % (resp.status, body))
    last = None
    for raw in resp:
        line = raw.decode("utf-8", "replace").strip()
        if not line:
            continue
        try:
            last = json.loads(line)
        except ValueError:
            continue
        _print_progress(last)
    conn.close()
    return last


def _print_progress(p):
    """Render one streamed progress/summary line."""
    filed = p.get("drawers", 0) + p.get("closets", 0) + p.get("kg_facts", 0) + p.get("tunnels", 0)
    total = p.get("total", 0)
    tail = "/%d" % total if total else ""
    msg = "  filed %d%s  (drawers %d, closets %d, facts %d, tunnels %d, skipped %d)" % (
        filed, tail, p.get("drawers", 0), p.get("closets", 0),
        p.get("kg_facts", 0), p.get("tunnels", 0), p.get("skipped", 0),
    )
    if p.get("done"):
        extra = "  hallways rebuilt: %d  in %dms" % (p.get("hallways", 0), p.get("elapsed_ms", 0))
        if p.get("error"):
            extra += "  (error: %s)" % p["error"]
        print(msg + "\n  done." + extra)
    else:
        # Carriage return keeps the running line in place in a terminal.
        sys.stdout.write("\r" + msg)
        sys.stdout.flush()


def main(argv=None):
    ap = argparse.ArgumentParser(
        description="Export a local mempalace and migrate it into the agentsmemory SaaS.",
    )
    ap.add_argument("--palace", default=None, help="Palace directory (default: your configured palace).")
    ap.add_argument("--kg-db", default=None, help="Knowledge-graph sqlite path (default: ~/.mempalace/knowledge_graph.sqlite3).")
    ap.add_argument("--mempalace-path", default=None, help="Path to the mempalace package if it is not already importable.")
    ap.add_argument("--file", default=None, help="Push a previously exported NDJSON bundle (skips reading a palace).")
    ap.add_argument("--out", default=None, help="Write the NDJSON bundle to this file.")
    ap.add_argument("--push", action="store_true", help="Stream the bundle to the SaaS /import endpoint.")
    ap.add_argument("--server", default=None, help="SaaS base URL, e.g. https://memory.example.com (with --push).")
    ap.add_argument("--token", default=os.environ.get("AGENTSMEMORY_TOKEN"), help="Project API token / Bearer (or set AGENTSMEMORY_TOKEN).")
    args = ap.parse_args(argv)

    if not args.out and not args.push:
        ap.error("nothing to do: pass --out FILE and/or --push")
    if args.push and (not args.server or not args.token):
        ap.error("--push requires --server and --token (or AGENTSMEMORY_TOKEN)")

    # Pushing a pre-exported bundle needs no palace and no mempalace package — it
    # is a pure upload, usable from any machine that holds the file.
    if args.file:
        if not args.push:
            ap.error("--file is a bundle to push; pass --push with --server and --token")
        print("  pushing %s to %s/import ..." % (args.file, args.server.rstrip("/")))
        push(args.file, args.server, args.token)
        return

    if args.mempalace_path:
        sys.path.insert(0, os.path.abspath(os.path.expanduser(args.mempalace_path)))

    try:
        mp = _Mempalace()
    except Exception as exc:
        raise SystemExit(
            "  could not import the mempalace package (%s).\n"
            "  Run this where mempalace is installed, or pass --mempalace-path." % exc
        )

    palace_path = os.path.abspath(os.path.expanduser(args.palace)) if args.palace else _default_palace_path()
    kg_db = os.path.abspath(os.path.expanduser(args.kg_db)) if args.kg_db else _default_kg_db()
    print("  palace: %s" % palace_path)
    print("  kg db:  %s" % (kg_db if os.path.isfile(kg_db) else "(none)"))

    # Write a file first when asked, then push from it (re-reading the palace
    # twice is wasteful and non-atomic for a large export).
    if args.out:
        n = write_file(records(mp, palace_path, kg_db), args.out)
        print("  wrote %d records -> %s" % (n, args.out))

    if args.push:
        source = args.out if args.out else records(mp, palace_path, kg_db)
        print("  pushing to %s/import ..." % args.server.rstrip("/"))
        push(source, args.server, args.token)


if __name__ == "__main__":
    main()
