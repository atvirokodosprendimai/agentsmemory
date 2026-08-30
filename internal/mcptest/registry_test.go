package mcptest_test

import (
	"encoding/json"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// scenarios is the end-to-end exercise set. It starts almost empty on purpose:
// the gate above reports what is missing, and that report is the measurement
// ADR-008 opens with. Entries are added by T3 and T4.
var scenarios = []mcptest.Scenario{
	{
		// ADR-036 T8. Two calls: file into the entry room, then bootstrap — so the
		// assertion is that ONE call returns what a session actually needs, not
		// that a handler returned something.
		Name:  "one call bootstraps a wing and carries every part of the protocol it replaces",
		Tools: []string{"am_add_drawer", "am_bootstrap"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_scenario", "room": "llm_init",
				"content": "BOOTSTRAP-MARKER read this before doing anything else in this wing",
			})
			out := h.MustCall(t, "am_bootstrap", map[string]any{"wing": "wing_scenario"})
			for _, part := range []string{"entry_point", "truncation", "BOOTSTRAP-MARKER"} {
				if !contains(out, part) {
					t.Errorf("the bootstrap does not carry %q, so it does not replace the protocol it claims to:\n%s", part, out)
				}
			}
			// A wing with no entry point still bootstraps rather than failing.
			empty := h.MustCall(t, "am_bootstrap", map[string]any{"wing": "wing_no_such_place"})
			if !contains(empty, "unknown_term") {
				t.Errorf("a wing with no entry point did not bootstrap distinguishably:\n%s", empty)
			}
		},
	},
	{
		// ADR-036 T7. Two calls, because a one-call scenario proves only that the
		// handler returned something: the drawer is FILED first, then the entry
		// point is asked, so the assertion is that the front door actually reaches
		// what was put behind it.
		Name:  "a wing reports its own entry point, and it reaches what was filed there",
		Tools: []string{"am_add_drawer", "am_entry_point"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			filed := h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_scenario", "room": "llm_init",
				"content": "WHAT MUST I LOAD AT THE START OF A SESSION? Start here.",
			})
			if !contains(filed, "has_edge") {
				t.Errorf("a filed drawer did not report whether it is reachable:\n%s", filed)
			}
			out := h.MustCall(t, "am_entry_point", map[string]any{"wing": "wing_scenario"})
			if !contains(out, "room:wing_scenario/llm_init") {
				t.Errorf("the wing did not name its own entry node:\n%s", out)
			}
			if !contains(out, "edges") {
				t.Errorf("the entry point reported no edges; it is a door onto a wall:\n%s", out)
			}
			// A wing that has no entry point says so rather than erroring — the
			// distinction T2's vocabulary exists to carry.
			empty := h.MustCall(t, "am_entry_point", map[string]any{"wing": "wing_no_such_place"})
			if !contains(empty, "unknown_term") {
				t.Errorf("a wing with no entry point did not say so distinguishably:\n%s", empty)
			}
		},
	},
	{
		Name:  "a filed memory is recalled by the question it answers",
		Tools: []string{"am_add_drawer", "am_search"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_scenario", "room": "decisions",
				"content": "rollbacks go through the previous image tag, never a rebuild",
			})
			if out := h.MustCall(t, "am_search", map[string]any{
				"query": "how do we roll back", "wing": "wing_scenario", "limit": 5,
			}); !contains(out, "previous image tag") {
				t.Errorf("a filed memory was not recalled by its own question:\n%s", out)
			}
		},
	},
	{
		Name:  "the wake-up call reports the workspace it is scoped to",
		Tools: []string{"am_add_drawer", "am_status"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_scenario", "room": "decisions", "content": "something to count",
			})
			out := h.MustCall(t, "am_status", map[string]any{})
			m := h.JSON(t, out)
			if m["ok"] != true {
				t.Errorf("am_status did not report ok:\n%s", out)
			}
			if m["total_drawers"] == nil {
				t.Errorf("am_status reported no drawer total, so it cannot ground a waking agent:\n%s", out)
			}
		},
	},
	{
		// The scenario that would have caught a capability shipped unreachable.
		//
		// am_update_drawer's handler read code_anchors from the moment it was
		// written, and the tool never DECLARED the argument. Every test was a
		// source grep or a unit test on the parser, all green, while no agent
		// reading the schema could learn the capability existed — which was the
		// exact complaint the work set out to fix. A review caught it; a call
		// through the tool surface would have caught it the same day.
		Name:  "a corrected memory can be re-anchored, and the new anchor is the one that sticks",
		Tools: []string{"am_add_drawer", "am_update_drawer", "am_list_anchors"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			out := h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_anchor", "room": "decisions",
				"content": "the retry budget is three attempts",
				"code_anchors": []any{map[string]any{
					"path": "internal/retry/retry.go", "snippet": "const budget = 3",
				}},
			})
			id := firstDrawerID(t, h, out)

			res := h.JSON(t, h.MustCall(t, "am_update_drawer", map[string]any{
				"id": id, "content": "the retry budget is five attempts",
				"reason": "the budget was raised after the timeout incident",
				"code_anchors": []any{map[string]any{
					"path": "internal/retry/retry.go", "snippet": "const budget = 5",
				}},
			}))
			newID, _ := res["drawer"].(map[string]any)["id"].(string)
			if newID == "" || newID == id {
				t.Fatalf("a correction must return the id of the record it minted: %v", res)
			}

			// On the CORRECTING record, and that is the whole assertion. A
			// correction supersedes, so the id the caller sent now names an ended
			// row: applying the anchors there would leave the correction with none
			// and this argument silently not doing the only thing it exists for.
			// am_list_anchors filters by WING, not by drawer, so the snippets are
			// picked out per drawer here. Passing an unknown drawer_id argument
			// instead would be silently ignored and the listing would return both
			// records' anchors — a check that cannot fail.
			all := h.MustCall(t, "am_list_anchors", map[string]any{"wing": "wing_anchor"})
			onNew := anchorSnippets(t, h, all, newID)
			if !containsAny(onNew, "budget = 5") {
				t.Errorf("the correcting record %s does not carry the new anchor; the anchors were "+
					"written to the record the update ENDED, so the correction ships unanchored "+
					"and this argument stops doing the only thing it exists for:\n%s", newID, all)
			}
			if containsAny(onNew, "budget = 3") {
				t.Errorf("the superseded anchor is still on the correction beside the new one; "+
					"REPLACE must not merge:\n%s", all)
			}
			// The ENDED record keeps its own, because it keeps its text — its pin is
			// still true of it. Until T5 filters recall, a wing-scoped listing shows
			// both, and that is the half-landed pair rather than a bug here.
			if onOld := anchorSnippets(t, h, all, id); !containsAny(onOld, "budget = 3") {
				t.Errorf("the superseded record lost its anchor:\n%s", all)
			}
		},
	},
	{
		// A refused argument must leave the memory as it found it. The first
		// version updated the drawer and THEN validated the anchors, so this call
		// changed the content and returned an error announcing a refusal.
		Name:  "a refused anchor list leaves the memory's content untouched",
		Tools: []string{"am_add_drawer", "am_update_drawer", "am_get_drawer"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			out := h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_atomic", "room": "decisions",
				"content": "the original sentence, which must survive a refused update",
			})
			id := firstDrawerID(t, h, out)

			h.MustRefuse(t, "am_update_drawer", map[string]any{
				"id": id, "content": "a replacement that must NOT be applied",
				"reason":       "this call is malformed and must change nothing",
				"code_anchors": []any{map[string]any{"paht": "typo.go", "snippet": "x"}},
			})

			got := h.MustCall(t, "am_get_drawer", map[string]any{"id": id})
			if !contains(got, "the original sentence") {
				t.Errorf("a refused call changed the content anyway, so the error announced a "+
					"refusal that did not happen:\n%s", got)
			}
			if contains(got, "must NOT be applied") {
				t.Errorf("the rejected content was written:\n%s", got)
			}
		},
	},
	{
		// REGRESSION — the chunk-0-only update.
		//
		// A memory over ChunkSize is several drawers sharing a parent, each with
		// its own embedding. am_update_drawer rewrote chunk 0 and left chunk 1
		// live, still returning the OLD text from search with nothing marking it
		// retracted. The update reported success while a false half of the memory
		// kept competing on equal footing with the correction.
		//
		// The first fix REFUSED, which made correction impossible for exactly the
		// long documents that most need it. ADR-038 T4 corrects the whole memory:
		// every old chunk ends and one new set is written.
		Name:  "regression: correcting a multi-chunk memory ends every chunk of it",
		Tools: []string{"am_add_drawer", "am_update_drawer", "am_search", "am_get_drawer"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			// Over ChunkSize (1600) so the memory really is several drawers; a
			// fixture below the threshold cannot reproduce this at all.
			old := "SUPERSEDED-MARKER never brief from the index file. " + filler(1900)
			out := h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_chunked", "room": "decisions", "content": old,
			})
			if n := drawerCount(t, h, out); n < 2 {
				t.Fatalf("fixture produced %d drawer(s); this scenario needs a multi-chunk memory "+
					"or it cannot see the defect it exists for", n)
			}
			id := firstDrawerID(t, h, out)

			h.MustCall(t, "am_update_drawer", map[string]any{
				"id": id, "content": "CORRECTED-MARKER always brief from the index file.",
				"reason": "the index file is always brief; the original had it backwards",
			})

			// Every chunk of the old memory must be ended, and the marker is placed
			// in the FIRST chunk while the check sweeps ALL of them: a supersede
			// that ended only the row it was given would leave the tail current,
			// which is the same shape as the defect this scenario exists for.
			got := h.MustCall(t, "am_search", map[string]any{
				"query": "brief from the index file", "wing": "wing_chunked", "limit": 20,
			})
			if !contains(got, "CORRECTED-MARKER") {
				t.Errorf("the correction is not searchable at all:\n%s", got)
			}
			if contains(got, "SUPERSEDED-MARKER") {
				t.Errorf("the superseded text is still on the default recall route, competing with "+
					"the correction that replaced it — and it kept its embedding, so it can "+
					"outrank it:\n%s", got)
			}
			// Every chunk of the old memory is ended, not just the one the caller
			// named. Read through the one explicit route, since the default one has
			// just been asserted not to carry them.
			hits := h.JSON(t, h.MustCall(t, "am_search", map[string]any{
				"query": "brief from the index file", "wing": "wing_chunked",
				"limit": 20, "include_history": true,
			}))
			var sawEnded bool
			for _, hid := range searchHitIDs(t, hits) {
				d := h.JSON(t, h.MustCall(t, "am_get_drawer", map[string]any{
					"id": hid, "include_history": true,
				}))
				body, _ := d["content"].(string)
				switch {
				case contains(body, "CORRECTED-MARKER"):
					if d["valid_to"] != nil {
						t.Errorf("the correction itself is marked ended: %v", d)
					}
				case contains(body, "SUPERSEDED-MARKER") || contains(body, "index file"):
					sawEnded = true
					if d["valid_to"] == nil {
						t.Errorf("a chunk of the superseded memory is still current: %v", d)
					}
				}
			}
			if !sawEnded {
				t.Errorf("include_history reached no chunk of the superseded memory, so the "+
					"per-chunk assertion above ran against nothing:\n%v", hits)
			}
		},
	},
	{
		// REGRESSION — the delete that orphaned child chunks, now the retraction
		// that must reach every chunk.
		//
		// Deleting a multi-chunk memory by its parent id removed the parent and
		// left the children embedded and searchable, pointing at a parent that no
		// longer existed. A get said it was gone; only a search could see it.
		// am_delete_drawer is gone (ADR-038 took erasure off the agent surface) and
		// am_invalidate_drawer inherits the same trap: end one row and the rest keep
		// answering with the claim that was just withdrawn.
		Name:  "regression: retracting a memory leaves no chunk current, by any route",
		Tools: []string{"am_add_drawer", "am_invalidate_drawer", "am_search", "am_get_drawer"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			// The marker must land in the LAST chunk, not the first. The defect
			// leaves the CHILDREN behind, so a marker in chunk 0 is ended by the
			// buggy retraction too and the scenario passes while the orphan survives.
			body := filler(1900) + " ORPHAN-MARKER the rollback procedure for the queue worker."
			out := h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_orphan", "room": "decisions", "content": body,
			})
			if n := drawerCount(t, h, out); n < 2 {
				t.Fatalf("fixture produced %d drawer(s); the orphaning defect only exists for a "+
					"multi-chunk memory", n)
			}
			id := firstDrawerID(t, h, out)

			h.MustCall(t, "am_invalidate_drawer", map[string]any{
				"id": id, "reason": "the rollback procedure was replaced by the runbook",
			})

			// Checked by BOTH routes, and the pair is the point: a get of the
			// parent said "gone" while the children were still embedded and
			// searchable. Either route alone would have reported this fixed.
			parent := h.JSON(t, h.MustCall(t, "am_get_drawer", map[string]any{
				"id": id, "include_history": true,
			}))
			if parent["ended_reason"] != "the rollback procedure was replaced by the runbook" {
				t.Errorf("the retracted parent carries no reason — a retraction without one is a "+
					"delete that kept the bytes: %v", parent)
			}
			// The marker sits in the LAST chunk precisely so this can see a child
			// that outlived the retraction: a retraction that ended only the row it
			// was handed leaves the tail searchable, which is the defect wearing a
			// new verb's name.
			if got := h.MustCall(t, "am_search", map[string]any{
				"query": "rollback procedure for the queue worker", "wing": "wing_orphan", "limit": 20,
			}); contains(got, "ORPHAN-MARKER") {
				t.Errorf("a chunk outlived the retraction and is still on the default recall "+
					"route:\n%s", got)
			}
			// And every chunk is reachable through the one explicit route, ended.
			hits := h.JSON(t, h.MustCall(t, "am_search", map[string]any{
				"query": "rollback procedure for the queue worker", "wing": "wing_orphan",
				"limit": 20, "include_history": true,
			}))
			for _, hid := range searchHitIDs(t, hits) {
				d := h.JSON(t, h.MustCall(t, "am_get_drawer", map[string]any{
					"id": hid, "include_history": true,
				}))
				if d["valid_to"] == nil {
					t.Errorf("chunk %v is still current after the retraction: %v", hid, d)
				}
			}
		},
	},
	{
		// REGRESSION — the anchor list that cleared instead of refusing.
		Name:  "regression: an all-unreadable anchor list refuses and keeps the old anchors",
		Tools: []string{"am_add_drawer", "am_update_drawer", "am_list_anchors"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			out := h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_keepanchor", "room": "decisions",
				"content": "the cache is invalidated on write",
				"code_anchors": []any{map[string]any{
					"path": "internal/cache/cache.go", "snippet": "func Invalidate() {",
				}},
			})
			id := firstDrawerID(t, h, out)

			h.MustRefuse(t, "am_update_drawer", map[string]any{
				"id":           id,
				"code_anchors": []any{map[string]any{"paht": "internal/cache/cache.go", "snippet": "x"}},
			})

			if got := h.MustCall(t, "am_list_anchors", map[string]any{"wing": "wing_keepanchor"}); !contains(got, "func Invalidate() {") {
				t.Errorf("a refused anchor list cleared the anchors it refused to replace:\n%s", got)
			}
		},
	},
	{
		// A tunnel is the only way a scoped recall can cross wings, so an unwoven
		// or unreadable one is a cross-project relationship that is invisible
		// forever. Audited 2026-08-20: every tunnel in the live palace had
		// access_count 0, and nothing had ever tested that they can be read back.
		Name:  "a tunnel woven between two wings is findable from either end",
		Tools: []string{"am_add_drawer", "am_create_tunnel", "am_list_tunnels", "am_find_tunnels"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			for _, w := range []string{"wing_app", "wing_infra"} {
				h.MustCall(t, "am_add_drawer", map[string]any{
					"wing": w, "room": "decisions", "content": "a decision filed in " + w,
				})
			}
			h.MustCall(t, "am_create_tunnel", map[string]any{
				"source_wing": "wing_app", "source_room": "decisions",
				"target_wing": "wing_infra", "target_room": "decisions",
				"label": "the deploy behaviour is explained by the infra decision",
			})

			if got := h.MustCall(t, "am_list_tunnels", map[string]any{"wing": "wing_app"}); !contains(got, "wing_infra") {
				t.Errorf("the tunnel is not listed from its source wing:\n%s", got)
			}
			// Tunnels are symmetric, so the far end must see it too — a link only
			// its author can find is not a link.
			if got := h.MustCall(t, "am_list_tunnels", map[string]any{"wing": "wing_infra"}); !contains(got, "wing_app") {
				t.Errorf("the tunnel is not listed from its TARGET wing; a link only its author "+
					"can find is not a link:\n%s", got)
			}
			// am_find_tunnels answers a different question — which ROOMS span two
			// wings, a passive connector rather than the woven link above. Asserted
			// against what it promises, not against what its name suggests.
			if got := h.MustCall(t, "am_find_tunnels", map[string]any{
				"wing_a": "wing_infra", "wing_b": "wing_app",
			}); !contains(got, "decisions") {
				t.Errorf("am_find_tunnels does not report the room both wings share:\n%s", got)
			}
		},
	},
	{
		// The knowledge graph is the only structure that can say a fact STOPPED
		// being true; search returns the best match and never the most current.
		Name:  "a fact added to the graph is queryable by its subject",
		Tools: []string{"am_kg_add", "am_kg_query", "am_kg_stats"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			h.MustCall(t, "am_kg_add", map[string]any{
				"subject": "queue-worker", "predicate": "deploys_to", "object": "batch-node-3",
				"valid_from": "2026-08-01",
			})
			if got := h.MustCall(t, "am_kg_query", map[string]any{"entity": "queue-worker"}); !contains(got, "batch-node-3") {
				t.Errorf("a fact added to the graph is not returned by a query for its subject:\n%s", got)
			}
			if got := h.MustCall(t, "am_kg_stats", map[string]any{}); contains(got, "\"triples\":0") {
				t.Errorf("the graph reports no triples after one was added:\n%s", got)
			}
		},
	},
	{
		// The diary is the cross-session thread, and it is read by exact agent
		// name — measured 2026-08-20, 89 entries had already fragmented across 11
		// names, so a session picking a different one reads none of the others.
		Name:  "a diary entry is readable by the agent that wrote it",
		Tools: []string{"am_diary_write", "am_diary_read"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			h.MustCall(t, "am_diary_write", map[string]any{
				"agent_name": "scenario-agent", "wing": "wing_diary",
				"entry": "tried the batch path first; it deadlocked on the locked table",
			})
			if got := h.MustCall(t, "am_diary_read", map[string]any{
				"agent_name": "scenario-agent", "wing": "wing_diary",
			}); !contains(got, "deadlocked on the locked table") {
				t.Errorf("an agent cannot read back its own diary entry:\n%s", got)
			}
			// A different name must read nothing — that IS the fragmentation, and
			// pinning it means the behaviour is a decision rather than a surprise.
			if got := h.MustCall(t, "am_diary_read", map[string]any{
				"agent_name": "a-different-agent", "wing": "wing_diary",
			}); contains(got, "deadlocked on the locked table") {
				t.Errorf("one agent's diary answered another agent's read:\n%s", got)
			}
		},
	},
	{
		Name:  "the palace can describe its own shape",
		Tools: []string{"am_add_drawer", "am_list_wings", "am_list_rooms", "am_get_taxonomy"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_shape", "room": "gotchas", "content": "a gotcha worth keeping",
			})
			for tool, want := range map[string]string{
				"am_list_wings": "wing_shape", "am_get_taxonomy": "wing_shape",
			} {
				if got := h.MustCall(t, tool, map[string]any{}); !contains(got, want) {
					t.Errorf("%s does not report %s:\n%s", tool, want, got)
				}
			}
			if got := h.MustCall(t, "am_list_rooms", map[string]any{"wing": "wing_shape"}); !contains(got, "gotchas") {
				t.Errorf("am_list_rooms does not report the room just written to:\n%s", got)
			}
		},
	},
	{
		Name:  "a near-duplicate is reported before it is filed twice",
		Tools: []string{"am_add_drawer", "am_check_duplicate"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			const text = "the reranker pool is taken off the fused head, so fusion decides what it sees"
			h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_dup", "room": "decisions", "content": text,
			})
			if got := h.MustCall(t, "am_check_duplicate", map[string]any{"content": text}); !contains(got, "reranker pool") {
				t.Errorf("filing the same text twice is not reported as a duplicate:\n%s", got)
			}
		},
	},
	{
		// The wake-up sequence an agent is told to run first. If any leg of it
		// fails, every session in every project starts blind, and nothing else in
		// the palace matters.
		Name:  "the wake-up sequence works end to end",
		Tools: []string{"am_skillset", "am_status", "am_get_aaak_spec", "am_add_drawer", "am_search"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			for _, tool := range []string{"am_skillset", "am_get_aaak_spec"} {
				if out := h.MustCall(t, tool, map[string]any{}); len(out) < 50 {
					t.Errorf("%s returned %d bytes — a waking agent is told to call this first and "+
						"gets nothing:\n%s", tool, len(out), out)
				}
			}
			h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_wake", "room": "decisions", "content": "the wake-up path is exercised",
			})
			h.MustCall(t, "am_status", map[string]any{})
			if out := h.MustCall(t, "am_search", map[string]any{
				"query": "wake-up path", "wing": "wing_wake", "limit": 5,
			}); !contains(out, "wake-up path is exercised") {
				t.Errorf("recall after the wake-up sequence returned nothing:\n%s", out)
			}
		},
	},
	{
		// Centralised skills are the team's shared conventions, and they exist in
		// no repository — the palace is their only copy. A write that cannot be
		// read back loses them silently.
		Name:  "a centralised skill written by one session is loadable by the next",
		Tools: []string{"am_update_skill", "am_list_skills", "am_load_skill"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			h.MustCall(t, "am_update_skill", map[string]any{
				"name": "scenario-conventions", "description": "how this team writes tests",
				"content": "SKILL-BODY-MARKER a test must be able to fail for the right reason",
			})
			if out := h.MustCall(t, "am_list_skills", map[string]any{}); !contains(out, "scenario-conventions") {
				t.Errorf("a written skill is not in the catalogue:\n%s", out)
			}
			if out := h.MustCall(t, "am_load_skill", map[string]any{"name": "scenario-conventions"}); !contains(out, "SKILL-BODY-MARKER") {
				t.Errorf("a skill in the catalogue does not load its body — the catalogue would then "+
					"advertise conventions nobody can read:\n%s", out)
			}
		},
	},
	{
		// A fact that STOPPED being true is the one thing search cannot express,
		// and invalidation is how the graph says so. If the ended fact still reads
		// as current, the graph is worse than absent — it is confidently wrong.
		Name:  "an invalidated fact stops reading as current",
		Tools: []string{"am_kg_add", "am_kg_invalidate", "am_kg_query", "am_kg_timeline"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			h.MustCall(t, "am_kg_add", map[string]any{
				"subject": "batch-runner", "predicate": "deploys_to", "object": "old-node",
				"valid_from": "2026-01-01",
			})
			h.MustCall(t, "am_kg_invalidate", map[string]any{
				"subject": "batch-runner", "predicate": "deploys_to", "object": "old-node",
				"ended": "2026-06-01", "reason": "the node was retired",
			})

			out := h.MustCall(t, "am_kg_query", map[string]any{"entity": "batch-runner"})
			if contains(out, `"current":true`) {
				t.Errorf("an invalidated fact still reads as current — a confidently wrong graph is "+
					"worse than no graph:\n%s", out)
			}
			// The timeline must still hold it: invalidation ends a fact, it does not
			// erase that the fact was once true.
			if out := h.MustCall(t, "am_kg_timeline", map[string]any{"entity": "batch-runner"}); !contains(out, "old-node") {
				t.Errorf("the timeline lost an ended fact; ending is not deleting:\n%s", out)
			}
		},
	},
	{
		// Anchors are what let a memory be checked against the code it describes.
		// A verdict that does not stick means every memory stays "unchecked" and
		// the staleness signal never fires.
		Name:  "an anchor verdict is recorded and readable",
		Tools: []string{"am_add_drawer", "am_list_anchors", "am_mark_anchors"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_verdict", "room": "decisions", "content": "the parser trims whitespace",
				"code_anchors": []any{map[string]any{
					"path": "internal/parse/parse.go", "snippet": "strings.TrimSpace(v)",
				}},
			})
			listed := h.MustCall(t, "am_list_anchors", map[string]any{"wing": "wing_verdict"})
			id := firstAnchorID(t, h, listed)

			h.MustCall(t, "am_mark_anchors", map[string]any{
				"verdicts": []any{map[string]any{"id": id, "status": "verified", "line": 42}},
			})
			if out := h.MustCall(t, "am_list_anchors", map[string]any{
				"wing": "wing_verdict", "status": "verified",
			}); !contains(out, id) {
				t.Errorf("a recorded verdict did not stick, so every memory stays unchecked and the "+
					"staleness signal never fires:\n%s", out)
			}
		},
	},
	{
		Name:  "the palace reports what it holds and what recall has done",
		Tools: []string{"am_add_drawer", "am_search", "am_memories_filed_away", "am_recall_stats", "am_graph_stats"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_stats", "room": "decisions", "content": "a memory to be counted",
			})
			h.MustCall(t, "am_search", map[string]any{"query": "a memory to be counted", "wing": "wing_stats"})
			for _, tool := range []string{"am_memories_filed_away", "am_recall_stats", "am_graph_stats"} {
				if out := h.MustCall(t, tool, map[string]any{}); len(out) < 10 {
					t.Errorf("%s returned nothing usable:\n%s", tool, out)
				}
			}
		},
	},
	{
		// Mining is how a blob becomes memories, and how closets — the curation
		// prior — come to exist at all. Measured 2026-08-20: this palace held ZERO
		// closets, which made six eval tables report a clean null for a prior
		// whose two arms were the same arm. A scenario that mines and then finds
		// the result is the difference between "the prior does nothing" and "the
		// prior was never fed".
		Name:  "mined text becomes findable memories, and the derived graph rebuilds",
		Tools: []string{"am_mine", "am_search", "am_recompute_graph", "am_list_hallways", "am_graph_stats"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			// Entity extraction is DICTIONARY-based — 59 known systems — not a
			// generic proper-noun extractor. A hallway is an entity co-occurrence
			// link between rooms, so a fixture whose text names no known system
			// produces zero hallways and an assertion over it can never fail.
			// Measured: "the Gateway Team owns the Upstream Timeout" extracts
			// nothing; "Kafka" and "NATS" extract. The first fixture here was the
			// former shape, and the mutation that made recompute return nothing
			// survived because of it.
			for _, r := range []struct{ room, text string }{
				// BOTH names must appear TWICE in EACH drawer: entityMinFreq is 2, so a
				// name seen once extracts nothing, and a drawer with fewer than two
				// entities is skipped entirely. Then hallwayMinCount requires the PAIR
				// in two drawers. The second fixture attempt named Kafka once here and
				// produced zero hallways for that reason alone.
				{"decisions", "We replaced Kafka with NATS. Kafka rebalancing stalled p99 during " +
					"rolling deploys, and NATS does not rebalance that way."},
				{"gotchas", "NATS consumers still stall when a deploy overlaps. Kafka had the same " +
					"shape of failure, so NATS was not a free win over Kafka."},
			} {
				h.MustCall(t, "am_mine", map[string]any{
					"wing": "wing_mined", "room": r.room, "source": r.room + ".md", "content": r.text,
				})
			}

			if out := h.MustCall(t, "am_search", map[string]any{
				"query": "why did we move off Kafka", "wing": "wing_mined", "limit": 5,
			}); !contains(out, "rebalancing stalled") {
				t.Errorf("mined text is not findable by a question it answers:\n%s", out)
			}

			h.MustCall(t, "am_recompute_graph", map[string]any{})

			// Assert the graph has CONTENT, not merely that a JSON envelope came
			// back. `len(out) > 10` is satisfied by {"count":0,"hallways":[]}, and
			// that is how a rebuild returning nothing passed.
			out := h.MustCall(t, "am_list_hallways", map[string]any{"wing": "wing_mined"})
			if contains(out, `"count":0`) {
				t.Errorf("rebuilding the derived graph produced no hallways over a corpus whose two "+
					"rooms share entities — the graph is wired and unfed:\n%s", out)
			}
			if stats := h.MustCall(t, "am_graph_stats", map[string]any{}); !contains(stats, "wing_mined") {
				t.Errorf("graph stats do not name the wing that was just mined:\n%s", stats)
			}
		},
	},
	{
		// A tunnel that cannot be FOLLOWED is a link nobody can walk. Audited
		// 2026-08-20: every tunnel in the live palace had access_count 0, so the
		// read path had never been exercised by anyone, ever.
		Name:  "a tunnel can be followed from the wing it leaves",
		Tools: []string{"am_add_drawer", "am_create_tunnel", "am_follow_tunnels", "am_traverse"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			for _, w := range []string{"wing_from", "wing_to"} {
				h.MustCall(t, "am_add_drawer", map[string]any{
					"wing": w, "room": "decisions", "content": "a decision filed in " + w,
				})
			}
			h.MustCall(t, "am_create_tunnel", map[string]any{
				"source_wing": "wing_from", "source_room": "decisions",
				"target_wing": "wing_to", "target_room": "decisions",
				"label": "FOLLOWED-LABEL the infra decision explains the app behaviour",
			})

			if out := h.MustCall(t, "am_follow_tunnels", map[string]any{
				"wing": "wing_from", "room": "decisions",
			}); !contains(out, "wing_to") {
				t.Errorf("a woven tunnel cannot be followed from the wing it leaves:\n%s", out)
			}
			if out := h.MustCall(t, "am_traverse", map[string]any{
				"start_room": "decisions", "max_hops": 2,
			}); len(out) < 10 {
				t.Errorf("traversing from a populated room returned nothing:\n%s", out)
			}
		},
	},
	{
		Name:  "listing drawers honours the registration wing and the explicit escape hatch",
		Tools: []string{"am_add_drawer", "am_list_drawers"},
		NewHarness: func(t *testing.T) *mcptest.Harness {
			return mcptest.NewWithWing(t, "wing_beta")
		},
		Run: exerciseListDrawersRegistrationWing,
	},
	{
		// ADR-038 T4, rung 3 for the two verbs that replaced erasure.
		//
		// am_delete_drawer, am_delete_tunnel, am_delete_hallway and am_delete_wing
		// were the four scenarios that stood here. They are gone from the agent
		// catalogue, and what stands in their place has to be exercised the same
		// way — a retraction that is declared and never called is the defect this
		// registry exists to catch, one door over from the erasure it replaced.
		Name:  "a retracted memory keeps its text and its reason, and a replaced fact leaves one value",
		Tools: []string{"am_add_drawer", "am_invalidate_drawer", "am_get_drawer", "am_kg_add", "am_kg_supersede", "am_kg_query"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			out := h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_gone", "room": "decisions",
				"content": "RETRACTED-MARKER we will ship the queue rewrite in Q3",
			})
			id := firstDrawerID(t, h, out)
			h.MustCall(t, "am_invalidate_drawer", map[string]any{
				"id": id, "reason": "the rewrite was cancelled, not rescheduled",
			})

			// Off the default route now (T5), and reached by the one explicit one.
			if refused := h.MustRefuse(t, "am_get_drawer", map[string]any{"id": id}); !contains(refused, "include_history") {
				t.Errorf("the refusal does not name the history route, so an agent holding this id "+
					"dead-ends on a record that plainly exists:\n%s", refused)
			}
			d := h.JSON(t, h.MustCall(t, "am_get_drawer", map[string]any{"id": id, "include_history": true}))
			body, _ := d["content"].(string)
			if !contains(body, "RETRACTED-MARKER") {
				t.Errorf("the retracted text is gone; ending is not deleting, and the version that "+
					"was withdrawn is the thing nothing else can recover: %v", d)
			}
			if d["ended_reason"] != "the rewrite was cancelled, not rescheduled" {
				t.Errorf("the retraction carries no reason, which is the only thing a delete could "+
					"not have done: %v", d)
			}
			if d["superseded_by"] != nil {
				t.Errorf("a retraction that replaces nothing invented a successor: %v", d)
			}

			// The fact half. A hand-rolled invalidate-then-add would leave both
			// values current until the end of the day, which is what issue #74
			// reproduced; kg_supersede does both ends at one instant.
			h.MustCall(t, "am_kg_add", map[string]any{
				"subject": "queue-worker", "predicate": "deploys to", "object": "old-rack",
			})
			h.MustCall(t, "am_kg_supersede", map[string]any{
				"subject": "queue-worker", "predicate": "deploys to",
				"old_object": "old-rack", "new_object": "new-rack",
				"reason": "the old rack was decommissioned",
			})
			facts := h.MustCall(t, "am_kg_query", map[string]any{
				"entity": "queue-worker", "status": "current",
			})
			if !contains(facts, "new-rack") {
				t.Errorf("the replacement fact is not current:\n%s", facts)
			}
			if contains(facts, "old-rack") {
				t.Errorf("both values are current after a supersede — that is the overlap the "+
					"single transaction exists to remove:\n%s", facts)
			}
		},
	},
	{
		Name:  "merging a wing moves both row enumeration and scoped vector recall",
		Tools: []string{"am_add_drawer", "am_merge_wing", "am_list_drawers", "am_search"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			for wing, content := range map[string]string{
				"wing_from":  "SOURCE-MERGE-MARKER the scheduler drains before deploy",
				"wing_to":    "TARGET-MERGE-MARKER the target already has a decision",
				"wing_other": "KEEPER-MERGE-MARKER an unrelated wing must remain",
			} {
				h.MustCall(t, "am_add_drawer", map[string]any{"wing": wing, "room": "decisions", "content": content})
			}

			h.MustRefuse(t, "am_merge_wing", map[string]any{"sources": []any{}, "target": "wing_to"})
			if got := h.MustCall(t, "am_list_drawers", map[string]any{"wing": "wing_from"}); !contains(got, "SOURCE-MERGE-MARKER") {
				t.Errorf("a refused empty merge changed the source wing:\n%s", got)
			}

			h.MustCall(t, "am_merge_wing", map[string]any{
				"sources": []any{"wing_from"}, "target": "wing_to",
			})
			if got := h.MustCall(t, "am_list_drawers", map[string]any{"wing": "wing_to", "limit": 20}); !contains(got, "SOURCE-MERGE-MARKER") || !contains(got, "TARGET-MERGE-MARKER") {
				t.Errorf("the target does not enumerate both its old memory and the merged memory:\n%s", got)
			}
			if got := h.MustCall(t, "am_search", map[string]any{
				"query": "scheduler drains before deploy", "wing": "wing_to", "limit": 20,
			}); !contains(got, "SOURCE-MERGE-MARKER") {
				t.Errorf("the merged row moved but its vector payload did not become searchable in the target:\n%s", got)
			}
			if got := h.MustCall(t, "am_list_drawers", map[string]any{"wing": "wing_from", "limit": 20}); contains(got, "SOURCE-MERGE-MARKER") {
				t.Errorf("the old source still enumerates the merged memory:\n%s", got)
			}
			if got := h.MustCall(t, "am_search", map[string]any{
				"query": "scheduler drains before deploy", "wing": "wing_from", "limit": 20,
			}); contains(got, "SOURCE-MERGE-MARKER") {
				t.Errorf("the old source still retrieves the merged memory from the vector index:\n%s", got)
			}
			if got := h.MustCall(t, "am_list_drawers", map[string]any{"wing": "wing_other"}); !contains(got, "KEEPER-MERGE-MARKER") {
				t.Errorf("the unrelated keeper wing changed during merge:\n%s", got)
			}
		},
	},
	{
		Name:  "the vector backend can be re-readied without losing what is stored",
		Tools: []string{"am_add_drawer", "am_reconnect", "am_search"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_reconnect", "room": "decisions",
				"content": "RECONNECT-MARKER the index must survive a re-ready",
			})
			h.MustCall(t, "am_reconnect", map[string]any{})
			if out := h.MustCall(t, "am_search", map[string]any{
				"query": "the index must survive a re-ready", "wing": "wing_reconnect", "limit": 5,
			}); !contains(out, "RECONNECT-MARKER") {
				t.Errorf("re-readying the backend lost what was already stored — a liveness check "+
					"that destroys data is worse than no liveness check:\n%s", out)
			}
		},
	},
}

func exerciseListDrawersRegistrationWing(t *testing.T, h *mcptest.Harness) {
	for _, wing := range []string{"wing_alpha", "wing_beta"} {
		h.MustCall(t, "am_add_drawer", map[string]any{
			"wing": wing, "room": "decisions", "content": "a decision in " + wing,
		})
	}
	h.MustCall(t, "am_add_drawer", map[string]any{
		"wing": "wing_alpha", "room": "inbox", "content": "alpha's private inbox item, not beta's business",
	})
	h.MustCall(t, "am_add_drawer", map[string]any{
		"wing": "wing_beta", "room": "inbox", "content": "beta's own inbox item",
	})

	got := h.MustCall(t, "am_list_drawers", map[string]any{"room": "inbox", "limit": 20})
	if contains(got, "alpha's private inbox item") {
		t.Errorf("listing with no wing returned another project's inbox:\n%s", got)
	}
	if !contains(got, "beta's own inbox item") {
		t.Errorf("listing with no wing did not return this registration's own inbox:\n%s", got)
	}
	if contains(got, "a decision in wing_beta") {
		t.Errorf("the room filter did not exclude this wing's other rooms:\n%s", got)
	}
	if all := h.MustCall(t, "am_list_drawers", map[string]any{"wing": "*", "room": "inbox", "limit": 20}); !contains(all, "alpha's private inbox item") {
		t.Errorf(`wing:"*" must still enumerate every wing:\n%s`, all)
	}
}

func createTunnel(t *testing.T, h *mcptest.Harness, source, target, label string) string {
	t.Helper()
	out := h.MustCall(t, "am_create_tunnel", map[string]any{
		"source_wing": source, "source_room": "decisions",
		"target_wing": target, "target_room": "decisions", "label": label,
	})
	id, _ := h.JSON(t, out)["id"].(string)
	if id == "" {
		t.Fatalf("created tunnel has no id:\n%s", out)
	}
	return id
}

func deleted(t *testing.T, out string) bool {
	t.Helper()
	var payload struct {
		Deleted bool `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode deletion result: %v\n%s", err, out)
	}
	return payload.Deleted
}

func mineHallwayCorpus(t *testing.T, h *mcptest.Harness, wing, marker string) {
	t.Helper()
	for _, fixture := range []struct{ room, text string }{
		{"decisions", marker + " We replaced Kafka with NATS. Kafka rebalancing stalled p99, and NATS avoided that rebalance."},
		{"gotchas", marker + " NATS consumers can still stall. Kafka had the same failure, so NATS was not a free win over Kafka."},
	} {
		h.MustCall(t, "am_mine", map[string]any{
			"wing": wing, "room": fixture.room, "source": wing + "-" + fixture.room + ".md", "content": fixture.text,
		})
	}
}

func firstListedID(t *testing.T, h *mcptest.Harness, out, field string) string {
	t.Helper()
	rows, _ := h.JSON(t, out)[field].([]any)
	if len(rows) == 0 {
		t.Fatalf("%s contains no rows:\n%s", field, out)
	}
	row, _ := rows[0].(map[string]any)
	id, _ := row["id"].(string)
	if id == "" {
		t.Fatalf("first %s row has no id:\n%s", field, out)
	}
	return id
}

func jsonNumber(t *testing.T, h *mcptest.Harness, out, field string) float64 {
	t.Helper()
	n, ok := h.JSON(t, out)[field].(float64)
	if !ok {
		t.Fatalf("%s is not numeric:\n%s", field, out)
	}
	return n
}

// firstAnchorID pulls the id of the first anchor a listing returned.
func firstAnchorID(t *testing.T, h *mcptest.Harness, out string) string {
	t.Helper()
	rows, _ := h.JSON(t, out)["anchors"].([]any)
	if len(rows) == 0 {
		t.Fatalf("no anchors listed:\n%s", out)
	}
	row, _ := rows[0].(map[string]any)
	id, _ := row["id"].(string)
	if id == "" {
		t.Fatalf("anchor has no id:\n%s", out)
	}
	return id
}

// filler pads a memory past ChunkSize so it is stored as several drawers.
func filler(n int) string {
	const s = "The queue worker drains in batches and retries with backoff. "
	out := ""
	for len(out) < n {
		out += s
	}
	return out
}

// drawerCount reports how many drawers an add produced, so a multi-chunk
// scenario can prove its fixture is actually multi-chunk before asserting
// anything about chunks.
func drawerCount(t *testing.T, h *mcptest.Harness, out string) int {
	t.Helper()
	rows, _ := h.JSON(t, out)["drawers"].([]any)
	return len(rows)
}

// anchorSnippets returns the snippets an am_list_anchors listing carries for ONE
// drawer. The listing filters by wing, so a scenario about which RECORD an anchor
// landed on has to separate them itself.
func anchorSnippets(t *testing.T, h *mcptest.Harness, out, drawerID string) []string {
	t.Helper()
	rows, _ := h.JSON(t, out)["anchors"].([]any)
	var got []string
	for _, raw := range rows {
		a, _ := raw.(map[string]any)
		if id, _ := a["drawer_id"].(string); id == drawerID {
			snippet, _ := a["snippet"].(string)
			got = append(got, snippet)
		}
	}
	return got
}

// containsAny reports whether any of the strings contains want.
func containsAny(haystack []string, want string) bool {
	for _, s := range haystack {
		if contains(s, want) {
			return true
		}
	}
	return false
}

// searchHitIDs pulls the drawer ids out of a decoded am_search result.
//
// It sweeps EVERY hit rather than the first, which is what makes the two
// supersede regressions above able to fail: both defects leave one chunk of a
// multi-chunk memory in the wrong state, and a check that reads only the top hit
// is blind to exactly that.
func searchHitIDs(t *testing.T, decoded map[string]any) []string {
	t.Helper()
	rows, _ := decoded["hits"].([]any)
	if len(rows) == 0 {
		t.Fatalf("the search returned no hits, so this scenario asserts nothing: %v", decoded)
	}
	ids := make([]string, 0, len(rows))
	for _, raw := range rows {
		hit, _ := raw.(map[string]any)
		if id, _ := hit["id"].(string); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// firstDrawerID pulls the id of the first drawer an add returned.
func firstDrawerID(t *testing.T, h *mcptest.Harness, out string) string {
	t.Helper()
	m := h.JSON(t, out)
	rows, ok := m["drawers"].([]any)
	if !ok || len(rows) == 0 {
		t.Fatalf("add returned no drawers:\n%s", out)
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected drawer shape:\n%s", out)
	}
	id, _ := row["id"].(string)
	if id == "" {
		t.Fatalf("drawer has no id:\n%s", out)
	}
	return id
}

// unobservable names tools this in-process harness cannot see the effect of.
// Each needs an external dependency; the gate rejects any other reason.
var unobservable = []mcptest.Unobservable{}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
