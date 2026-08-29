package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"log/slog"

	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
	"go.opentelemetry.io/otel/attribute"
)

// registerDrawers wires the core memory-loop tools — the WRITE/FILE, SEARCH/RECALL
// and the remaining STATUS/ADMIN families of the Python contract — onto the MCP
// server. Every handler shares the admit() preamble (auth + monthly metering) and
// is scoped to the resolved tenant's TeamID, so a token can only ever touch its
// own workspace's memories.
func registerDrawers(reg *registrar, drawers *palace.Service, usageSvc *usage.Service, scopeSearchToWing bool) {
	registerAddDrawer(reg, drawers, usageSvc)
	registerGetDrawer(reg, drawers, usageSvc)
	registerUpdateDrawer(reg, drawers, usageSvc)
	registerInvalidateDrawer(reg, drawers, usageSvc)
	registerListDrawers(reg, drawers, usageSvc, scopeSearchToWing)
	registerSearch(reg, drawers, usageSvc, scopeSearchToWing)
	registerCheckDuplicate(reg, drawers, usageSvc)
	registerListWings(reg, drawers, usageSvc)
	registerListRooms(reg, drawers, usageSvc, scopeSearchToWing)
	registerGetTaxonomy(reg, drawers, usageSvc)
	registerGetAAAKSpec(reg, drawers, usageSvc)
	registerReconnect(reg, drawers, usageSvc)
}

// responseBudget bounds the TOTAL content one tool response may carry, in runes.
//
// It is shared, and being shared is the point. It was written for search alone as
// responseBudget, and am_list_drawers — which returns WHOLE drawers at a default
// limit of 50, so ~80,000 runes at ChunkSize — went unbounded beside it for exactly
// as long. One transport ceiling deserves one number: a second constant of the same
// value is the same knowledge in two places, and the second copy is the one that
// does not get updated.
//
// snippet_chars=0 means "give me whole memories" and that is a documented,
// deliberate request. What was missing is a ceiling on the PAGE: a memory may be
// up to palace.MaxContentLength (100,000 runes) and MaxSearchLimit is 100, so a
// single search could assemble ~10M runes — against roughly 1,920 before whole
// memories were returned at all. Nothing capped it.
//
// ⚠ WHERE THE NUMBER COMES FROM, stated because the honest answer is not
// "measured here". The figure this was originally justified by — a client
// truncating somewhere around 40-45KB — traces to a note in model/draf1.md and to
// nothing executable: no probe, no test, no command in this tree establishes it.
// Nor could there be one on this side. Truncation is the CLIENT's behaviour, it is
// not published, and it moves between versions, so a server-side gate asserting a
// specific ceiling would be pinning someone else's constant.
//
// That observation is why a bound EXISTS. It is not the bound. A threshold whose
// provenance is prose can motivate having a limit and must never be the limit, or
// the number outlives whatever was true when somebody said it.
//
// What justifies 40,000 is ours to check: it is roughly 10k tokens, a large share
// of any session's context, and a search page or a room listing is a NAVIGATION
// aid rather than the payload — the caller asked what is there, and fetches what
// it wants by id. A response past this point is not more generous, it is spending
// context the caller did not choose to spend. The failure it also happens to avert
// on truncating clients is a bonus, not the rationale.
//
// A response is filled in the order it was ranked or listed, so the budget spends
// itself on what the caller most likely wanted and the tail degrades to a bounded
// head rather than the page being cut. Nothing is ever dropped silently: a trimmed
// record says so and carries its full length.
const responseBudget = 40_000

// headWithin returns the opening of content bounded by BOTH a preferred head size
// and whatever is left of the response budget, and reports whether it had to cut.
//
// The second bound is the one that was missing. A budget checked only before the
// content is added still overshoots by the size of every replacement head — with a
// limit of 100 and a 400-rune head that is 40,000 runes past a 40,000 budget, which
// is the ceiling doubled by the very branch that exists to respect it. Trimming to
// `remaining` makes the bound hold for the whole response instead of for the
// records that happened to fit.
func headWithin(content string, head, remaining int) (string, bool) {
	runes := []rune(content)
	if len(runes) <= remaining {
		return content, false
	}
	if head > remaining {
		head = remaining
	}
	if head < 0 {
		head = 0
	}
	return string(runes[:head]), true
}

// drawerView is the agent-facing JSON shape of a drawer. It omits TeamID (the
// caller already knows its own scope) and gives every field an explicit snake_case
// tag so the wire format is stable regardless of Go field names.
type drawerView struct {
	ID          string   `json:"id"`
	Wing        string   `json:"wing"`
	Room        string   `json:"room"`
	SourceFile  string   `json:"source_file"`
	ChunkIndex  int      `json:"chunk_index"`
	Content     string   `json:"content"`
	Entities    []string `json:"entities,omitempty"`
	ParentID    string   `json:"parent_id,omitempty"`
	FiledAt     string   `json:"filed_at"`
	ContentDate string   `json:"content_date,omitempty"`
	// The validity window, omitted while the memory is current. A reader that
	// cannot see these has no way to tell a live memory from a retracted one, and
	// until recall filters (T5) an ended record still comes back from every
	// default route — so the marking is the only thing standing between a reader
	// and a claim the team withdrew.
	ValidTo      string `json:"valid_to,omitempty"`
	EndedReason  string `json:"ended_reason,omitempty"`
	SupersededBy string `json:"superseded_by,omitempty"`
	// What this record REPLACED, on the default route. A session about to redo a
	// rejected thing does not know to ask for history, so the live record carries
	// it; superseded_reason is capped so a page cannot grow with the corpus.
	Supersedes       string `json:"supersedes,omitempty"`
	SupersededReason string `json:"superseded_reason,omitempty"`
	// Set when the response budget trimmed this drawer's content. Both fields or
	// neither: "truncated" without the original length tells a caller something is
	// missing and not how much, which is not enough to decide whether to fetch it.
	Truncated bool `json:"content_truncated,omitempty"`
	// content_length, not full_length: a search hit already reports the original
	// size under that name, and one concept with two wire names on two tools is a
	// distinction a caller has to learn for nothing.
	FullLength int `json:"content_length,omitempty"`
}

// toView projects a domain Drawer onto its wire shape.
func toView(d palace.Drawer) drawerView {
	return drawerView{
		ID:               d.ID,
		Wing:             d.Wing,
		Room:             d.Room,
		SourceFile:       d.SourceFile,
		ChunkIndex:       d.ChunkIndex,
		Content:          d.Content,
		Entities:         d.Entities,
		ParentID:         d.ParentID,
		FiledAt:          d.FiledAt,
		ContentDate:      d.ContentDate,
		ValidTo:          d.ValidTo,
		EndedReason:      d.EndedReason,
		SupersededBy:     d.SupersededBy,
		Supersedes:       d.Supersedes,
		SupersededReason: d.SupersededReason,
	}
}

// jsonResult marshals v into a text tool result. A marshal failure is an internal
// bug, not an agent error, so it is surfaced as a tool error rather than panicking.
func jsonResult(v any) *mcp.CallToolResult {
	out, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError("internal: failed to encode result")
	}
	return mcp.NewToolResultText(string(out))
}

// registerAddDrawer: file a verbatim memory. Oversized content is chunked, each
// chunk embedded and stored; the response reports the drawers created.
func registerAddDrawer(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("add_drawer",
		// The chunk threshold is interpolated rather than written out, because this
		// sentence shipped for months claiming ~800 — the frozen Python miner's
		// figure, which chunk.go deliberately diverged from. Three independent
		// sessions read the wrong number off this description before anyone read
		// the constant. A description that restates a value is a second source of
		// truth; deriving it means the drift cannot recur.
		mcp.WithDescription(fmt.Sprintf(
			"File a verbatim memory (drawer) into a wing/room. Content over %d runes is chunked into "+
				"several drawers sharing a parent; re-adding the same source is idempotent. ⚠That "+
				"threshold binds at CREATION: a multi-chunk memory can be CORRECTED (am_update_drawer "+
				"with content supersedes the whole memory) but never MOVED, because moving one chunk "+
				"would split the memory across two scopes and no single search would return all of it. "+
				"A memory created at or under %d runes stays ONE row and can be relocated for life.",
			palace.ChunkSize, palace.ChunkSize)),
		mcp.WithString("wing", mcp.Description("Project namespace the memory belongs to. Optional when this MCP was registered for a project — then it defaults to that project's wing.")),
		mcp.WithString("room", mcp.Required(), mcp.Description("Aspect within the wing, e.g. \"backend\" or \"decisions\".")),
		mcp.WithString("content", mcp.Required(), mcp.Description("The verbatim text to remember — stored exactly, never summarised.")),
		mcp.WithString("source_file", mcp.Description("Optional provenance of the content (a path or label).")),
		mcp.WithString("content_date", mcp.Description("Optional date the memory is about (e.g. 2026-06-26).")),
		mcp.WithArray("code_anchors", mcp.Description(
			"Optional: pin this memory to the code it is about, as [{\"path\":\"internal/x/y.go\",\"snippet\":\"<verbatim lines>\",\"repo\":\"<optional label>\"}]. "+
				"Paste the exact code, NOT a line number — line numbers move on every edit above them. When the snippet later "+
				"disappears from the file, search marks this memory STALE instead of letting the next session act on a fact "+
				"that stopped being true. Anchor whenever a memory explains a specific piece of code.")),
		mcp.WithBoolean("confirm_new_wing", mcp.Description(
			"Set true to file an inbox item into a wing that holds no memories yet. Without it that "+
				"combination is refused, because it is what an undeliverable handoff looks like: a "+
				"target wing named for the direction of travel (wing_to-x) instead of the project "+
				"(wing_x) is a wing no session will ever look in. Pass it when the project really "+
				"has no memories filed for it yet.")),
	)
	reg.addWrite(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		wing, err := wingFor(ctx, req.GetString("wing", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		room, err := req.RequireString("room")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		content, err := req.RequireString("content")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// An inbox item into a wing that holds nothing is what an undeliverable
		// handoff looks like — see handoffRefusal. Refuse here, while the filer
		// still has the context to correct the name; nobody notices later.
		if refusal := handoffRefusal(ctx, drawers, t.TeamID, wing, room,
			req.GetBool("confirm_new_wing", false)); refusal != "" {
			return mcp.NewToolResultError(refusal), nil
		}
		created, err := drawers.Add(ctx, t.TeamID, palace.AddInput{
			Wing:        wing,
			Room:        room,
			Content:     content,
			SourceFile:  req.GetString("source_file", ""),
			ContentDate: req.GetString("content_date", ""),
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		views := make([]drawerView, len(created.Drawers))
		for i, d := range created.Drawers {
			views[i] = toView(d)
		}
		out := map[string]any{"ok": true, "chunks": len(created.Drawers), "drawers": views}
		// Whether the filing is REACHABLE, not merely stored. Measured 2026-08-26,
		// 57 of 1,985 drawers carried any edge and 0 were named as a triple
		// object, so "filed" and "findable by traversal" had quietly become
		// different things and nothing said which one had just happened.
		if len(created.Drawers) > 0 {
			out["has_edge"] = created.Drawers[0].HasEdge
			out["edge_derived"] = created.Drawers[0].EdgeDerived
		}

		// Anchors pin the FIRST chunk: it is the parent handle for a multi-chunk
		// write, and the one search returns as the memory's identity.
		if anchors := parseAnchors(req.GetArguments()["code_anchors"]); len(anchors) > 0 && len(created.Drawers) > 0 {
			n, err := drawers.AddAnchors(ctx, t.TeamID, created.Drawers[0].ID, anchors)
			if err != nil {
				// The memory is already filed; an anchor failure must not present
				// as a failed write, so it is reported beside the success.
				out["anchor_error"] = err.Error()
			} else {
				out["code_anchors"] = n
			}
		}
		if created.PendingEmbedding {
			// Say it in a field the caller can branch on AND in prose it will read
			// out loud. A memory that is stored but not yet searchable looks
			// identical to a healthy one from here, and the operator is the only
			// one who can fix what caused it.
			out["pending_embedding"] = true
			out["warning"] = pendingEmbeddingWarning
		}
		return jsonResult(out), nil
	})
}

// parseAnchors reads the code_anchors argument: a list of {path, snippet, repo}
// objects. It is tolerant by design — an unparseable entry is skipped rather than
// failing the write, because the memory itself is worth more than its anchor.
func parseAnchors(raw any) []palace.AnchorInput {
	out, _, _ := parseAnchorList(raw)
	return out
}

// parseAnchorList is parseAnchors with the one distinction the tolerant version
// cannot make: whether the argument was a LIST at all.
//
// Tolerance is right where it was written — an unreadable entry means "no
// anchors added" and the memory is worth more than its anchor. It is wrong at a
// REPLACE, where the same empty result means "delete the anchors this memory
// already has". `code_anchors: {…}` instead of `[{…}]` is an ordinary mistake for
// an LLM caller, and without this the write path would treat it as a deliberate
// clear and report success — an unknown recorded as a definite negative,
// destroying what it was built to protect. This repository has fixed that exact
// shape at the read end already.
//
// A genuine `[]` still reads fine, so a deliberate clear is unaffected.
//
// It also reports how many entries were SENT, which is the other half of the
// same distinction. A non-empty list whose entries are all malformed parses to
// readable-and-empty and would otherwise read as a deliberate clear — and since
// most callers send exactly one anchor, "every entry malformed" is usually just
// "the one anchor I sent had a typo", the likeliest way to get an entry wrong at
// all. The caller asked to SET anchors, none could be read, and deleting the
// existing ones is the opposite of the intent.
func parseAnchorList(raw any) ([]palace.AnchorInput, bool, int) {
	list, ok := raw.([]any)
	if !ok {
		return nil, false, 0
	}
	out := make([]palace.AnchorInput, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		path, _ := m["path"].(string)
		snippet, _ := m["snippet"].(string)
		repo, _ := m["repo"].(string)
		if strings.TrimSpace(path) == "" || strings.TrimSpace(snippet) == "" {
			continue
		}
		out = append(out, palace.AnchorInput{Repo: repo, Path: path, Snippet: snippet})
	}
	return out, true, len(list)
}

// anchorReplacement decides what a code_anchors argument means at a REPLACE,
// returning either the anchors to write or a non-empty refusal to send back.
//
// It exists as a function rather than inline at the call site because the two
// refusals are the whole behaviour: a source-grep check that the call site
// "looks right" passes happily against a guard disarmed with && false, which is
// the same component-tested/selection-untested shape this file already fixed
// once. A function can be driven by a test; a call site can only be read.
//
// Both refusals protect the same thing — an argument the caller got wrong must
// not delete the anchors a memory already has. An unreadable VALUE (an object
// where a list belongs) and an unreadable LIST (every entry malformed, which is
// most often the single anchor sent having a typo) both parse to nothing, and
// nothing is indistinguishable from a deliberate []. A list with one bad row
// among several is not this case: it is readable, something survived, and the
// bad row is dropped.
func anchorReplacement(raw any) ([]palace.AnchorInput, string) {
	anchors, readable, sent := parseAnchorList(raw)
	if !readable {
		return nil, "code_anchors must be a LIST of {path, snippet, repo?} objects; the value sent could not be " +
			"read as one. Refusing rather than clearing, because an unreadable argument and a " +
			"deliberate \"remove the anchors\" look identical once parsed — send [] if you meant to clear them"
	}
	if sent > 0 && len(anchors) == 0 {
		return nil, fmt.Sprintf(
			"code_anchors carried %d entr(ies) and none could be read — each needs a non-empty "+
				"\"path\" and \"snippet\". Refusing rather than clearing: you asked to set anchors, "+
				"so deleting the ones this memory has would be the opposite of that. Send [] if you "+
				"meant to remove them", sent)
	}
	return anchors, ""
}

// pendingEmbeddingWarning is the one sentence a caller must pass on when a write
// was stored without its vector: the memory is safe, it is simply not findable
// yet, and something outside this server has to be fixed for that to change.
const pendingEmbeddingWarning = "stored, but NOT searchable yet: the embedder could not be reached, " +
	"so this memory is queued for background indexing. It will become searchable once the embedder is " +
	"running again (check the Ollama server the agentsmemory server points at). Tell the user."

// annotateSearchID records a caller-supplied search_id on the current tool span.
//
// Separate from the handler so it can be driven by a test: the handler needs a
// live palace, and the behaviour worth pinning is "the id reaches the span",
// which needs no storage at all.
func annotateSearchID(ctx context.Context, req mcp.CallToolRequest) {
	sid := strings.TrimSpace(req.GetString("search_id", ""))
	if sid == "" {
		return
	}
	// Every other am.* string in the tree is derived server-side; this one is
	// whatever a client sent. ADR-025 keeps query text off spans, and a caller
	// can put query text — or anything else — in this field, so the shape is
	// checked before the value reaches a collector rather than after.
	//
	// A rejected id is recorded as a rejection instead of being dropped in
	// silence: ADR-028 defers on "the first week a non-test client sends one",
	// and clients sending malformed ids would otherwise read as no adoption at
	// all, which is the opposite conclusion.
	if !palace.ValidSearchID(sid) {
		telemetry.Annotate(ctx, attribute.Bool("am.search_id_rejected", true))
		return
	}
	telemetry.Annotate(ctx, attribute.String("am.search_id", sid))
}

// recordFetchJoin durably records that a fetch returned something, against the
// recall the caller named.
//
// Sibling of annotateSearchID rather than an extension of it, deliberately.
// That function's whole contract is "the id reaches the span", which needs no
// storage and is tested without a palace; widening its signature to carry a team
// and a service would cost that test its independence. This one needs both, and
// runs only where a fetch has already SUCCEEDED — a request for an id that does
// not resolve is not a click, and recording one would put misses in the
// numerator of every ratio derived from the table (ADR-028 T3).
func recordFetchJoin(ctx context.Context, drawers *palace.Service, teamID string, req mcp.CallToolRequest, drawerID string, whole bool) {
	sid := strings.TrimSpace(req.GetString("search_id", ""))
	if sid == "" {
		return
	}
	// Validity is re-checked here rather than assumed from the span annotator:
	// the two run independently, and a durable row is worth more care than an
	// attribute. RecordFetch refuses a malformed id too — this is belt and
	// braces on the value that reaches storage.
	drawers.RecordFetch(ctx, teamID, sid, drawerID, whole)
}

// registerGetDrawer: fetch one drawer by id.
func registerGetDrawer(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("get_drawer",
		mcp.WithDescription("Fetch a drawer by its id. A memory longer than ~1600 characters is stored as several chunks and a search returns the ONE that matched; pass whole=true to get every chunk of that memory, in order, so you can read the note as it was written. A response you did NOT ask to be whole says whether it is one: content_truncated is set with content_length, the whole memory's rune count, whenever the drawer you asked for is a chunk of a longer memory — so a fragment is never mistakable for a complete short memory. Complete it by calling this tool again with the same id and whole=true; that is the only completion path, and no cursor or offset exists."),
		mcp.WithString("id", mcp.Required(), mcp.Description("The drawer id returned by am_add_drawer or am_search.")),
		mcp.WithBoolean("whole", mcp.Description("Return every chunk of the memory this drawer belongs to, in order, instead of just this one. Any chunk's id works — you do not need the first.")),
		mcp.WithString("search_id", mcp.Description("Optional: the search_id of the am_search page that led you to this memory. It is recorded on the request's trace span AND, since ADR-028 T3, durably against the drawer this call returned — so the recall that sent you here becomes a relevance signal. Nothing changes in what you get back. A fetch that does not resolve records nothing, and an id that is not the shape am_search mints is refused rather than stored.")),
		mcp.WithBoolean("include_history", mcp.Description("Also return memories that have been RETRACTED or superseded (default false). Off by default because an ended record keeps its embedding: without the filter a withdrawn claim competes with the correction that replaced it, and can outrank it. Turn it on to audit what a wing used to say. Every returned record carries valid_to and ended_reason, so history is never mistaken for current, plus superseded_by when something replaced it — and a CURRENT record carries supersedes and superseded_reason naming what it replaced and why, so a session about to redo a rejected thing sees that without asking for history.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Inert for STORAGE — recording the join durably is its own task with its
		// own trigger — but not invisible. The id goes on the tool span, so a
		// search followed by a fetch is one traceable pair today, at the cost of
		// one attribute and no schema change.
		//
		// The first version of this line threw the id away (`_ = ...`). That
		// shipped a signal whose adoption could not be observed by the very
		// instrument this repository had just made mandatory at deploy time: an
		// agent sent the id, the server accepted it, and the trace showed a bare
		// `am.tool ... ran` with nothing linking it to any recall. A change that
		// adds a signal has to extend the instrument in the same commit, or its
		// own trigger — "the first week a non-test client sends one" — is
		// unanswerable.
		annotateSearchID(ctx, req)
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		id, err := req.RequireString("id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// A chunked memory had no read path that could return it whole: the query
		// existed (repo.MemoryChunks) and was called only by update and delete.
		// An agent handed one chunk of a long note had no second call to complete
		// it, which is why collapsing a search page to one hit per memory could
		// not ship on its own.
		history := req.GetBool("include_history", false)
		if req.GetBool("whole", false) {
			chunks, err := drawers.GetMemoryAnyVersion(ctx, t.TeamID, id, history)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			views := make([]drawerView, 0, len(chunks))
			for _, c := range chunks {
				views = append(views, toView(c))
			}
			// One fetch, not one per chunk: a caller asking for a whole memory
			// read ONE memory, and counting its chunks would weight long notes
			// higher in every count derived from this.
			if len(chunks) > 0 {
				recordFetchJoin(ctx, drawers, t.TeamID, req, chunks[0].ID, true)
			}
			return jsonResult(map[string]any{"chunks": views, "count": len(views)}), nil
		}
		get := drawers.Get
		if history {
			get = drawers.GetAnyVersion
		}
		d, err := get(ctx, t.TeamID, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		recordFetchJoin(ctx, drawers, t.TeamID, req, d.ID, false)
		v := toView(d)
		// ADR-044 F-2: a fragment a caller cannot tell is a fragment.
		//
		// Without whole:true this returns ONE chunk, and until now it said nothing
		// about that — content_truncated and content_length both absent, which is
		// byte-for-byte what a complete short memory looks like. The team's own
		// operating protocol had to warn about it in prose ("it looks complete"),
		// and a warning an agent must have read is what this field replaces.
		//
		// Keyed on the chunk COUNT, never on ParentID: the root chunk of a
		// multi-chunk memory has no parent and chunk_index 0, so a parent test
		// leaves exactly the case this exists for unmarked.
		//
		// Fails OPEN and says so in the trace: a size lookup that errors must not
		// turn a working read into an error, but an UNMARKED fragment is the defect
		// itself, so it cannot pass silently either.
		if full, n, err := drawers.MemorySize(ctx, t.TeamID, d.ID); err != nil {
			telemetry.Annotate(ctx, attribute.Bool("am.memory_size_failed", true))
			slog.Warn("memory size lookup failed; drawer returned without its partial marking",
				"error", err, "drawer", d.ID)
		} else if n > 1 {
			partialWithFetchID(&v, full)
		}
		return jsonResult(v), nil
	})
}

// registerUpdateDrawer: correct or relocate a memory. Which one it is depends on
// which field moves, and the two differ in whether the id survives (ADR-038):
// content SUPERSEDES — a new record, the old one ended with the caller's reason,
// and the two linked — while a wing/room move keeps the id, because minting a new
// record for a relocation would invalidate every anchor, tunnel and fact pointing
// at it in exchange for nothing.
func registerUpdateDrawer(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("update_drawer",
		mcp.WithDescription("Correct or relocate a memory. Sending content is a CORRECTION: it writes a NEW record, ends the old one with your reason, and links them — so the id changes and the old text stays readable by its own id, because the version that was replaced is the thing nothing else can recover. Sending only wing/room is a relocation and keeps the id. Only supplied fields are modified."),
		mcp.WithString("id", mcp.Required(), mcp.Description("The drawer id to correct or move. Any chunk's id: a correction replaces the WHOLE memory.")),
		mcp.WithString("content", mcp.Description(fmt.Sprintf(
			"New verbatim content, at most %d characters, which SUPERSEDES the record rather than editing it: "+
				"you get back a new id and the id you sent stays readable, ended, and linked to the new one. "+
				"Long content is chunked exactly as add_drawer chunks it, so a multi-chunk memory is "+
				"correctable — every old chunk ends and one new set is written. Requires reason.",
			palace.MaxContentLength))),
		mcp.WithString("reason", mcp.Description("REQUIRED with content: why the old record stopped applying. Not a changelog — the sentence a reader six months from now needs in order to know whether this correction still holds. \"obsolete\" tells them nothing that valid_to did not already say.")),
		mcp.WithString("wing", mcp.Description("Move the memory to this wing. With content, the correcting record is filed here.")),
		mcp.WithString("room", mcp.Description("Move the memory to this room. With content, the correcting record is filed here.")),
		mcp.WithArray("code_anchors", mcp.Description(
			"REPLACE this memory's code anchors, as [{\"path\":\"internal/x/y.go\",\"snippet\":\"<verbatim lines>\",\"repo\":\"<optional label>\"}]. "+
				"Send [] to remove them all. Omit the field to leave them untouched. "+
				"With content, these are applied to the CORRECTING record, not the one being ended. "+
				"A correction carries the old record's anchors forward as unchecked, so send this only "+
				"to point the correction somewhere else — the staleness check that protects a memory is "+
				"otherwise what marks the correction out of date, which is the case this exists for.")),
	)
	reg.addWrite(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		id, err := req.RequireString("id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// Distinguish "field omitted" from "field set to empty" by presence in the
		// raw arguments, so update_drawer only touches what the agent actually sent.
		args := req.GetArguments()
		patch := palace.DrawerPatch{}
		if _, ok := args["content"]; ok {
			v := req.GetString("content", "")
			patch.Content = &v
		}
		patch.Reason = req.GetString("reason", "")
		if _, ok := args["wing"]; ok {
			v := req.GetString("wing", "")
			patch.Wing = &v
		}
		if _, ok := args["room"]; ok {
			v := req.GetString("room", "")
			patch.Room = &v
		}
		// Anchors are REPLACED, not merged. This exists for the case that
		// motivated it: a memory is corrected, and its old anchor still pins the
		// old text — so the staleness check meant to protect the memory is what
		// marks the correction out of date. Merging would leave both live.
		//
		// Validated BEFORE anything is written. The first version updated the
		// drawer and then checked the anchors, so a call carrying new content and
		// a malformed anchor list changed the content and returned an error
		// announcing that it had refused — a caller reading that error and
		// retrying would apply the content twice. An argument the caller got
		// wrong must leave the memory as it found it.
		raw, wantsAnchors := args["code_anchors"]
		var anchors []palace.AnchorInput
		if wantsAnchors {
			var refusal string
			if anchors, refusal = anchorReplacement(raw); refusal != "" {
				return mcp.NewToolResultError(refusal), nil
			}
		}

		res, err := drawers.Update(ctx, t.TeamID, id, patch)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out := map[string]any{"drawer": toView(res.Drawer)}
		if res.Supersedes != "" {
			out["supersedes"] = res.Supersedes
			out["reason"] = res.Reason
			out["ended_at"] = res.EndedAt
		}
		if wantsAnchors {
			// On the id the update RETURNED, never the one the caller sent. Once a
			// content change supersedes, those are different rows, and writing to the
			// caller's id anchors the record that was just ended — leaving the
			// correction with no anchors and this parameter silently not doing the
			// only thing it was written for.
			n, aerr := drawers.ReplaceAnchors(ctx, t.TeamID, res.Drawer.ID, anchors)
			if aerr != nil {
				return mcp.NewToolResultError(aerr.Error()), nil
			}
			out["code_anchors"] = n
		}
		return jsonResult(out), nil
	})
}

// registerInvalidateDrawer: retract a memory that nothing replaces.
//
// It is what stands where delete_drawer stood, and the pair is the point of
// ADR-038: an agent that finds a memory wrong needs to say so, and the verb it
// was given destroyed the record instead. Retraction is now the only thing an
// agent can do; erasure moved to the operator, who has the database file.
//
// Separate from update_drawer because plenty of retractions replace nothing —
// "we are not doing this after all" has no successor, and forcing one makes an
// agent invent a placeholder memory to express an absence.
func registerInvalidateDrawer(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("invalidate_drawer",
		mcp.WithDescription("Retract a memory that nothing replaces: it stops being current, its text stays readable by id, and the reason is recorded. Use update_drawer with content instead when something DOES replace it. The whole memory is retracted — a memory over the chunk size is several drawers sharing a parent, and retracting one of them would leave the rest current and still answering with the claim you just withdrew. This is not a delete: nothing is destroyed, and only an operator with the database can erase."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Any chunk's id of the memory to retract.")),
		mcp.WithString("reason", mcp.Required(), mcp.Description("Why it stopped being true. This is the whole difference between a retraction and a delete — the row already records THAT it ended, and only you can say why.")),
	)
	reg.addWrite(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		id, err := req.RequireString("id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		reason, err := req.RequireString("reason")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if err := drawers.InvalidateDrawer(ctx, t.TeamID, id, reason); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"ok": true, "ended": id, "reason": reason}), nil
	})
}

// registerListDrawers: paginate a team's drawers, optionally filtered by wing/room.
func registerListDrawers(reg *registrar, drawers *palace.Service, usageSvc *usage.Service, scopeSearchToWing bool) {
	tool := newTool("list_drawers",
		mcp.WithDescription("List drawers (newest first), optionally narrowed to a wing and/or room, with limit/offset paging. A listing carries whole drawers, so a large page is bounded: once the response budget is spent the remaining drawers carry as much of their opening as the budget still allows — possibly none — with content_truncated and content_length set, and a note says how many. Nothing is dropped — read any of them in full with am_get_drawer(id, whole=true), or narrow with room/limit. Omitted, scoped to this registration's default_wing only when one is configured and SEARCH_SCOPE is not workspace; otherwise omission lists every wing. Pass \"*\" to list every wing deliberately."),
		mcp.WithString("wing", mcp.Description("Only drawers in this wing. Omitted, scoped to this registration's default_wing only when one is configured and SEARCH_SCOPE is not workspace; otherwise every wing. Pass \"*\" for every wing deliberately."), searchWingProperty()),
		mcp.WithString("room", mcp.Description("Only drawers in this room.")),
		mcp.WithNumber("limit", mcp.Description("Max drawers to return (default 50).")),
		mcp.WithNumber("offset", mcp.Description("Number of drawers to skip (default 0).")),
		mcp.WithBoolean("include_history", mcp.Description("Also return memories that have been RETRACTED or superseded (default false). Off by default because an ended record keeps its embedding: without the filter a withdrawn claim competes with the correction that replaced it, and can outrank it. Turn it on to audit what a wing used to say. Every returned record carries valid_to and ended_reason, so history is never mistaken for current, plus superseded_by when something replaced it — and a CURRENT record carries supersedes and superseded_reason naming what it replaced and why, so a session about to redo a rejected thing sees that without asking for history.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		// Resolved exactly as a recall is. am_search has scoped to the
		// registration's wing since scoping landed and this did not, so a listing
		// that named no wing enumerated EVERY wing — including other projects'
		// inboxes, which is the call am_status recommends to a waking agent. A
		// scope one enumeration route ignores is not a scope.
		wing, err := searchWingFor(ctx, req.GetString("wing", ""), scopeSearchToWing)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		list := drawers.List
		if req.GetBool("include_history", false) {
			list = drawers.ListAnyVersion
		}
		listed, err := list(ctx, t.TeamID,
			wing, req.GetString("room", ""),
			req.GetInt("limit", 50), req.GetInt("offset", 0))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// A listing returns whole drawers, and fifty of them at ChunkSize is roughly
		// twice a search page's whole allowance, spent without the caller asking.
		// On a client that truncates it is worse than costly: the result arrives as
		// nothing and reads as an empty room. Trim the tail to a bounded head
		// instead, and say so on every record that was cut.
		views := make([]drawerView, len(listed))
		spent, trimmed := 0, 0
		for i, d := range listed {
			views[i] = toView(d)
			// The head, not a window: a listing has no query to centre on, and the
			// opening line is what its author wrote to say what the memory is.
			if head, cut := headWithin(d.Content, palace.DefaultSnippetChars, responseBudget-spent); cut {
				views[i].Content = head
				partialWithFetchID(&views[i], len([]rune(d.Content)))
				trimmed++
			}
			spent += len([]rune(views[i].Content))
		}
		out := map[string]any{"drawers": views, "count": len(views)}
		if trimmed > 0 {
			out["note"] = fmt.Sprintf(
				"%d of %d drawer(s) exceeded this response's size budget and carry their opening "+
					"openings instead of their full text, and the last of them may carry none at all "+
					"(content_truncated, with content_length). Read any "+
					"of them in full with am_get_drawer(id, whole=true), or narrow the listing with "+
					"room/limit. The budget is a context bound, not a page count: a listing tells you WHAT is there and you fetch what you want by id.",
				trimmed, len(views))
		}
		return jsonResult(out), nil
	})
}

// newSearchHitView maps a domain hit onto the view an agent receives.
//
// Extracted from the loop it used to be inlined in, because an inline composite
// literal is unreachable from a test: a mutant that populated blended_score from
// RerankScore — the exact confusion ADR-028 T2 exists to end — SURVIVED the
// task's fence, since nothing could assert what the RENDER produced. The
// ordering property was covered in the domain and the field's presence by the
// reflect gate, and neither can see a wrong assignment between them.
func newSearchHitView(h palace.SearchHit) searchHitView {
	return searchHitView{
		drawerView:    toView(h.Drawer),
		MemoryID:      h.MemoryID,
		Score:         h.Score,
		BM25:          h.BM25,
		ClosetBoost:   h.ClosetBoost,
		Distance:      h.Distance,
		RerankScore:   h.RerankScore,
		Reranked:      h.Reranked,
		Blended:       h.Blended,
		ChunksMatched: h.ChunksMatched,
		StaleIndex:    h.StaleIndex,
		Corrections:   h.Corrections,
	}
}

// searchHitView is one ranked search result: the drawer plus its scores.
type searchHitView struct {
	drawerView
	// MemoryID is the stable logical-memory handle. ID above remains the best
	// matching stored passage for compatibility and may be a child chunk.
	MemoryID string `json:"memory_id"`
	// StaleIndex marks a recall the index did not serve: the hits came from the
	// source of truth's own vector path because the index was behind it, and a
	// rebuild is in flight. Omitted on a healthy recall so responses stay
	// byte-identical to before.
	StaleIndex  bool    `json:"stale_index,omitempty"`
	Score       float64 `json:"score"`        // fused hybrid rank (vector + BM25 + closet boost), higher is better
	BM25        float64 `json:"bm25_score"`   // raw lexical BM25 component, for transparency
	ClosetBoost float64 `json:"closet_boost"` // closet rank boost folded into score, for transparency
	Distance    float64 `json:"distance"`     // raw cosine distance in [0,2], lower is closer
	// RerankScore is the cross-encoder's relevance for this hit, present only when
	// a reranker is configured (omitted otherwise so an unconfigured deployment's
	// results are byte-identical to before). It is reported rather than folded
	// into score because the two are not on the same scale — an agent reading the
	// page should be able to see which signal moved a hit.
	RerankScore float64 `json:"rerank_score,omitempty"`
	// Reranked says a cross-encoder actually SCORED this hit. It exists because
	// rerank_score's absence was ambiguous four ways: no reranker configured, a
	// reranker at weight 0, a hit below the pool cutoff that was never scored, and
	// a cross-encoder that genuinely returned 0.0 all produced the same missing
	// key. The domain has carried this bool since ADR-006 T4 made the telemetry
	// honest; the agent-facing surface discarded it, so the one reader who acts on
	// the answer could not see it.
	// NOT omitempty. The whole reason this field exists is that an absent
	// rerank_score meant four things at once; dropping the false case would leave
	// three of them merged. A hit that says reranked:false is a hit the
	// cross-encoder did not score, stated.
	Reranked bool `json:"reranked"`
	// Blended is the score the page was ORDERED by — BlendRerank's weighted
	// combination of the pool-normalised fused and rerank scores. It is here
	// because without it a returned order is unexplainable from the response: a
	// page is routinely NOT monotonic in rerank_score, which is correct (the
	// fused score is the better judge of vocabulary and carries half the weight)
	// and reads exactly like a reranker that silently did not run. Reported as a
	// suspected bug on 2026-08-25 and diagnosable only by reading BlendRerank.
	//
	// omitempty for the same reason rerank_score is: a hit outside the scored
	// pool has no blend, and 0.0 would claim it had one.
	Blended float64 `json:"blended_score,omitempty"`
	// ChunksMatched is how many chunks of this memory were in the ranked pool.
	// A memory that matched in four places is stronger evidence than one that
	// matched in one, and ADR-013's collapse would otherwise destroy that signal
	// silently — which it did, for exactly as long as this field existed in the
	// domain and not on the wire.
	ChunksMatched int `json:"chunks_matched,omitempty"`
	// Truncated says the content above is a snippet around the match, not the
	// whole memory — fetch it with am_get_drawer when the snippet is not enough.
	//
	// It is kept and it is no longer the field to read: it is true for 98% of
	// hits, and a flag that almost never varies carries no information. Coverage
	// and Regions below are what an agent can act on.
	Truncated  bool `json:"content_truncated,omitempty"`
	FullLength int  `json:"content_length,omitempty"`
	// Coverage is the fraction of the memory `content` shows, 0..1.
	//
	// NOT omitempty: 0 is a real and important value — it means the snippet shows
	// none of this memory — and this codebase has already shipped one field whose
	// absence meant four different things at once.
	Coverage float64 `json:"content_coverage"`
	// Regions are the OTHER places in this memory that matched, verbatim, in
	// position order. Present only when there is more than one: a single region is
	// what `content` already carries, and repeating it would teach a reader to
	// skip the field.
	Regions []regionView `json:"regions,omitempty"`
	// Identity is the memory's own first line — what its author wrote to say what
	// this is. It is a label to choose by, and nothing generated it.
	Identity string `json:"identity,omitempty"`
	// Anchors are the code this memory was written about, with the verdict of the
	// last verification pass. Stale is the summary an agent should branch on.
	Anchors []anchorView `json:"code_anchors,omitempty"`
	Stale   bool         `json:"stale,omitempty"`
	// Corrections are the retracts/supersedes/qualifies edges pointing at this
	// record. Without them a page that bootstraps perfectly still serves a record
	// somebody has already contradicted, and says nothing about it.
	Corrections []palace.Correction `json:"corrections,omitempty"`
}

// regionView is one matching part of a memory as search reports it: the verbatim
// text, how many query terms fell inside, and where it starts.
//
// Verbatim is the contract, not an implementation detail. add_drawer promises
// content is "stored exactly, never summarised", and an agent acting on prose
// this server wrote would be that promise broken at the read end.
type regionView struct {
	Text  string `json:"text"`
	Terms int    `json:"terms_matched"`
	Start int    `json:"start"`
}

// anchorView is one code anchor as search reports it.
type anchorView struct {
	Path      string `json:"path"`
	Status    string `json:"status"` // unchecked | verified | drifted | missing
	Line      int    `json:"line,omitempty"`
	CheckedAt string `json:"checked_at,omitempty"`
}

// registerSearch: hybrid recall over a team's drawers — vector candidates
// re-ranked by a vector+BM25 blend (closet boost joins with the mining phase).
func registerSearch(reg *registrar, drawers *palace.Service, usageSvc *usage.Service, scopeSearchToWing bool) {
	tool := newTool("search",
		mcp.WithDescription("Semantically recall distinct memories most similar to a query. Optionally filter by wing/room and a max cosine distance. Each hit carries blended_score: the value the page was actually ordered by, combining the cross-encoder and the fused lexical/vector score. It is POOL-RELATIVE — comparable between hits on one page, meaningless across pages, and not to be averaged. A page is often not monotonic in rerank_score, which is the blend working rather than a reranker that failed. content_coverage is always present and reports how much of the memory you are seeing: the primary window AND every region rendered beside it, counted once where they overlap. It changed meaning on 2026-08-29 — it used to count the window alone and under-reported, so a threshold calibrated before then is comparing against a different number. Fields that appear only when they apply: content_truncated with content_length when a memory was trimmed; regions, the other windows of the same memory that matched, when more than one did; chunks_matched, how many of its chunks did; content_date when the memory is about a different day than it was filed; code_anchors with a status when the memory pins source that may have drifted; stale on a hit whose code anchors no longer match the code they pin — the one summary to branch on, because a recalled sentence about changed code reads as knowledge either way; and stale_index on the page when the search index is behind the store, which means the answer may be missing recent writes rather than that nothing was written."),
		mcp.WithString("query", mcp.Required(), mcp.Description("What to recall (max 250 chars).")),
		mcp.WithBoolean("include_history", mcp.Description("Also return memories that have been RETRACTED or superseded (default false). Off by default because an ended record keeps its embedding: without the filter a withdrawn claim competes with the correction that replaced it, and can outrank it. Turn it on to audit what a wing used to say. Every returned record carries valid_to and ended_reason, so history is never mistaken for current, plus superseded_by when something replaced it — and a CURRENT record carries supersedes and superseded_reason naming what it replaced and why, so a session about to redo a rejected thing sees that without asking for history.")),
		mcp.WithNumber("limit", mcp.Description("Max distinct memories after chunk collapse in the legacy control (before ranking in the memory-level treatment), 1-100 (default 5).")),
		mcp.WithString("wing", mcp.Description("Restrict to this wing. Omitted, a recall is scoped to the wing this MCP registration was created for — but ONLY if it was registered with one: am_status reports it as default_wing, and when that is empty (or SEARCH_SCOPE=workspace) omitting the argument searches every wing instead. Pass a wing to look at one project, or \"*\" to search EVERY wing deliberately — worth doing when the question is about something shared, such as an infrastructure decision that explains an application's behaviour."), searchWingProperty()),
		mcp.WithString("room", mcp.Description("Restrict to this room.")),
		mcp.WithNumber("max_distance", mcp.Description("Drop results farther than this cosine distance (0-2, default 1.5; 0 disables).")),
		mcp.WithNumber("snippet_chars", mcp.Description(
			"How much of each hit's text to return, as a window centred on the match (default 400). "+
				"Recall is paid for in your context window: a page of full-length memories costs several thousand tokens, "+
				"and most of it is text you did not need. Pass 0 for whole memories, or fetch any single one in full with am_get_drawer.")),
		mcp.WithString("context", mcp.Description("Optional background context — what you are working on. Sharpens re-ranking when a reranker is configured; ignored otherwise. It does not change which drawers are retrieved, only how they are ordered.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// How much of each memory the caller will see. Recorded from the REQUEST,
		// before admission, for the same reason annotateSearchID sits there in
		// am_get_drawer: it is knowable immediately, and it decides the page's
		// CONTENT rather than its order — two recalls with byte-identical ranking
		// attributes can hand back a 400-rune window and a whole memory, with
		// nothing on the search span telling them apart.
		telemetry.Annotate(ctx, attribute.Int("am.snippet_chars", req.GetInt("snippet_chars", palace.DefaultSnippetChars)))
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		query, err := req.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		wing, err := searchWingFor(ctx, req.GetString("wing", ""), scopeSearchToWing)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		page, err := drawers.SearchPage(ctx, t.TeamID, palace.SearchQuery{
			Query:       query,
			Wing:        wing,
			Room:        req.GetString("room", ""),
			Limit:       req.GetInt("limit", palace.DefaultSearchLimit),
			MaxDistance: req.GetFloat("max_distance", palace.DefaultMaxDistance),
			Context:     req.GetString("context", ""),

			IncludeHistory: req.GetBool("include_history", false),
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		hits := page.Hits
		snippetChars := req.GetInt("snippet_chars", palace.DefaultSnippetChars)
		// spent/overBudget bound the WHOLE-memory expansion. See responseBudget.
		spent, overBudget := 0, 0
		views := make([]searchHitView, len(hits))
		ids := make([]string, len(hits))
		for i, h := range hits {
			views[i] = newSearchHitView(h)
			ids[i] = h.MemoryID
			fullContent := h.MemoryContent
			if fullContent == "" {
				fullContent = h.Drawer.Content
			}
			views[i].Content = fullContent
			views[i].Identity = palace.MemoryIdentity(fullContent)
			if snippetChars > 0 {
				// The window is centred on the query's own terms, so what comes
				// back is the part that matched rather than only the memory's
				// heading. The input is the whole memory, so the authored opening is
				// available even when a child chunk nominated the result.
				if snippet := palace.SnippetWithHead(fullContent, query, snippetChars, true); snippet != fullContent {
					views[i].Content = snippet
					views[i].Truncated = true
					views[i].FullLength = len([]rune(fullContent))

					// Every OTHER place this memory matched, and the line its author
					// wrote to say what it is.
					//
					// content above is one window, chosen by a score that saturates:
					// once a window holds the query's terms every other window holding
					// them ties, and ties go to the earliest position. On this corpus a
					// memory opens with a header line carrying the date, project and
					// subject, so the opening wins by construction and the body is never
					// shown — measured across nine real queries, 7 of 9 chosen windows
					// began within 130 runes of the start and 0 of 9 were beaten by a
					// later one.
					//
					// content_truncated already said a memory was cut. It is true for
					// 98% of hits, which is why it cannot be acted on: an agent cannot
					// fetch five whole memories and nothing told it which one hid the
					// answer. These fields are what let it choose.
					regions := palace.SnippetRegions(fullContent, query, snippetChars)
					if len(regions) > 1 {
						// One region is what content already is; repeating it would spend
						// the page on a duplicate and teach a reader to skip the field.
						for _, r := range regions {
							views[i].Regions = append(views[i].Regions, regionView{
								Text: r.Text, Terms: r.Score, Start: r.Start,
							})
						}
					}
				}
			}
			// snippet_chars=0 asks for whole memories, and that request is honoured
			// until the page as a whole stops being deliverable — see
			// responseBudget. Past it the remaining hits fall back to a bounded
			// window, marked truncated with the full length like any other trim, so
			// a caller can tell it happened and ask for the rest by id.
			if snippetChars <= 0 && spent+len([]rune(fullContent)) > responseBudget {
				// The window is still query-centred where it fits; the bound below
				// then decides whether even that fits.
				views[i].Content = palace.SnippetWithHead(fullContent, query, palace.DefaultSnippetChars, true)
				views[i].Truncated = true
				views[i].FullLength = len([]rune(fullContent))
				overBudget++
			}

			// ⚠ THE BOUND APPLIES ON EVERY PATH, not only to whole memories, and it
			// counts the regions too. snippet_chars is caller-supplied and unclamped,
			// so `limit=100, snippet_chars=100000` rendered a multi-megabyte page
			// while the budget above looked on: it was conditioned on the ONE branch
			// somebody tested. Regions are additional rendered text and were never in
			// the total at all, so a page could pass the check and still ship several
			// times the budget.
			if trimmed, cut := headWithin(views[i].Content, len([]rune(views[i].Content)), responseBudget-spent); cut {
				views[i].Content = trimmed
				if !views[i].Truncated {
					views[i].Truncated = true
					views[i].FullLength = len([]rune(fullContent))
					overBudget++
				}
				// Regions are a second copy of matching passages. Once the budget is
				// gone they are the first thing to drop: content is the answer, regions
				// are only how to find more of it.
				views[i].Regions = nil
			}
			spent += len([]rune(views[i].Content))
			for _, r := range views[i].Regions {
				spent += len([]rune(r.Text))
			}

			// Coverage is set for EVERY hit, including snippet_chars=0. Otherwise
			// "the caller requested and received the whole memory" reports the same
			// zero as "the caller saw none of it".
			views[i].Coverage = coveredRunes(views[i].Content, views[i].Regions, fullContent)
		}
		// Staleness travels WITH the memory. A recalled sentence about code that
		// has since changed is the one failure mode a confident agent cannot catch
		// on its own — it reads as knowledge either way.
		stale := 0
		anchors, anchorErr := drawers.AnchorsForMemories(ctx, t.TeamID, ids)
		if anchorErr != nil {
			// Fails OPEN — a page without staleness marks beats no page at all — but
			// it must not fail SILENTLY. Every `stale` flag vanishes from the response
			// and the enclosing am.tool span still ends `ran`, because traceTool
			// inspects only the handler's Go error and res.IsError, and both are clean
			// here. So a page whose staleness marking was lost is indistinguishable,
			// to the caller AND to the trace, from one where nothing was stale — and
			// staleness is the single failure mode a confident agent cannot catch on
			// its own, since a recalled sentence about changed code reads as knowledge
			// either way.
			telemetry.Annotate(ctx, attribute.Bool("am.anchors_failed", true))
			slog.Warn("anchor lookup failed; page returned without staleness marks",
				"error", anchorErr, "memories", len(ids))
		}
		if anchorErr == nil {
			for i := range views {
				for _, a := range anchors[ids[i]] {
					views[i].Anchors = append(views[i].Anchors, anchorView{
						Path: a.Path, Status: a.Status, Line: a.Line, CheckedAt: a.CheckedAt,
					})
					if a.Stale() {
						views[i].Stale = true
					}
				}
				if views[i].Stale {
					stale++
				}
			}
		}
		// search_id is page-level and present even when count is 0: a recall that
		// found nothing still ran and still wrote its row, and that is the page
		// most worth tracing. It is the primary key of the search_events row.
		out := map[string]any{"hits": views, "count": len(views), "search_id": page.SearchID}
		// The fact block, rendered BESIDE the hits. Three keys, because a match
		// this recall did not return is reported one of two ways and never as
		// silence: the WING that holds it, when provenance makes that derivable
		// and an agent can go and query it, or a COUNT when it does not.
		//
		// Silence is indistinguishable from "nothing is filed", which is the
		// failure this whole surface exists to remove — and on today's corpus the
		// unplaceable case is the majority: 90 of 196 triples resolve to a drawer,
		// measured 2026-08-26.
		if !page.Facts.Empty() {
			out["facts"] = page.Facts.Facts
			out["elsewhere_wings"] = page.Facts.ElsewhereWings
			out["unlocatable_facts"] = page.Facts.Unlocatable
		}
		// Say it, rather than letting the caller infer it from a truncation flag on
		// hits it did not ask to have truncated. A silent cap on a "give me
		// everything" request is the shape that teaches an agent the palace is
		// missing content it actually holds.
		if overBudget > 0 {
			// The caller is told (below) and now so is the trace. A page that
			// silently delivered less than was asked for is the same shape as the
			// anchor failure a few lines down: honoured request, degraded answer,
			// span still `ran`.
			telemetry.Annotate(ctx, attribute.Int("am.whole_memory_over_budget", overBudget))
			out["note"] = fmt.Sprintf(
				"whole memories were requested and the last %d hit(s) exceeded this response's "+
					"size budget, so they are windowed instead (content_truncated carries "+
					"content_length). Fetch any of them in full with am_get_drawer(id, whole=true), "+
					"or narrow the search — a larger response spends context you did not ask "+
					"budget is a context bound: a page this size is most of a session's context, and "+
					"most of it is text you did not ask for.", overBudget)
		}
		// A zero-hit page from a wing that holds nothing is not a miss, and the two
		// were indistinguishable: same count, same empty list, same sub-second
		// reply. Measured against real queries, that confusion produced every hard
		// failure in the sample.
		if len(views) == 0 {
			if note, _ := emptyWingNote(ctx, drawers, t.TeamID, wing); note != "" {
				out["note"] = note
			}
		}
		if stale > 0 {
			out["stale_hits"] = stale
			out["warning"] = "some hits are marked STALE: the code they were written about has changed since. " +
				"Re-read that code before acting on the memory, and re-file the memory if it is now wrong."
		}
		return jsonResult(out), nil
	})
}

// registerCheckDuplicate: is content near-identical to an existing drawer?
func registerCheckDuplicate(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("check_duplicate",
		mcp.WithDescription("Check whether content is near-identical to an existing drawer before filing it."),
		mcp.WithString("content", mcp.Required(), mcp.Description("The candidate content to test.")),
		mcp.WithNumber("threshold", mcp.Description("Cosine-similarity threshold for a duplicate (default 0.9).")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		content, err := req.RequireString("content")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		res, err := drawers.CheckDuplicate(ctx, t.TeamID, content, req.GetFloat("threshold", palace.DefaultDupThreshold))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out := map[string]any{"is_duplicate": res.IsDuplicate, "similarity": res.Similarity}
		if res.Drawer != nil {
			out["drawer"] = toView(*res.Drawer)
		}
		return jsonResult(out), nil
	})
}

// registerListWings: per-wing drawer/room counts.
func registerListWings(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("list_wings",
		mcp.WithDescription("List the team's wings with how many drawers and distinct rooms each holds."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		wings, err := drawers.Wings(ctx, t.TeamID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"wings": wings, "count": len(wings)}), nil
	})
}

// registerListRooms: per-room drawer counts, optionally within one wing.
func registerListRooms(reg *registrar, drawers *palace.Service, usageSvc *usage.Service, scopeSearchToWing bool) {
	tool := newTool("list_rooms",
		mcp.WithDescription("List the team's rooms with drawer counts, optionally restricted to one wing. Omitted, scoped to this registration's default_wing only when one is configured and SEARCH_SCOPE is not workspace; otherwise omission lists every wing. Pass \"*\" to list every wing deliberately."),
		mcp.WithString("wing", mcp.Description("Only rooms within this wing. Omitted, scoped to this registration's default_wing only when one is configured and SEARCH_SCOPE is not workspace; otherwise every wing. Pass \"*\" for every wing deliberately."), searchWingProperty()),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		// Resolved through searchWingFor, not taken raw. Audited 2026-08-20 by
		// RUNNING it against two projects in one workspace: naming no wing
		// enumerated every wing, so one project's room names and drawer counts were disclosed to another. am_search and
		// am_list_drawers resolve identically, and an enumeration that does not is
		// a hole in the scope those two enforce.
		wing, err := searchWingFor(ctx, req.GetString("wing", ""), scopeSearchToWing)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		rooms, err := drawers.Rooms(ctx, t.TeamID, wing)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"rooms": rooms, "count": len(rooms)}), nil
	})
}

// registerGetTaxonomy: the wing -> rooms tree with counts.
func registerGetTaxonomy(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("get_taxonomy",
		mcp.WithDescription("Return the team's memory taxonomy: every wing with its rooms and counts."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		tax, err := drawers.GetTaxonomy(ctx, t.TeamID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(tax), nil
	})
}

// registerGetAAAKSpec: the static AAAK dialect reference. It needs no storage, so
// it still meters (to keep tool behaviour uniform) but reads nothing per-team.
func registerGetAAAKSpec(reg *registrar, _ *palace.Service, usageSvc *usage.Service) {
	tool := newTool("get_aaak_spec",
		mcp.WithDescription("Return the AAAK compressed-memory dialect spec agents use for diary and closet lines."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if _, errResult, ok := admit(ctx, usageSvc); !ok {
			return errResult, nil
		}
		return jsonResult(map[string]any{"spec": palace.AAAKSpec}), nil
	})
}

// registerReconnect ensures the tenant's vector namespace exists and confirms
// the backend is reachable. The server has no cached client to drop (unlike the
// Python tool), but EnsureNamespace may create backend state, so reconnect stays
// write-gated even though repeating it is idempotent.
func registerReconnect(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("reconnect",
		mcp.WithDescription("Ensure the workspace's vector namespace exists and confirm the backend is reachable. This idempotent operation is write-gated because it may create backend state."),
	)
	reg.addWrite(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		if err := drawers.Reconnect(ctx, t.TeamID); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"ok": true, "note": "vector namespace ready, backend reachable"}), nil
	})
}

// disclosedRange is one half-open rune range of a memory that a response
// actually put in front of its caller.
type disclosedRange struct{ start, end int }

// coveredRunes reports the fraction of a memory a response disclosed: the
// primary window AND every region rendered beside it, counted once where they
// overlap.
//
// It replaces `len(content) / len(fullContent)`, which counted the window alone
// while the regions sat rendered next to it in the same response. Measured
// 2026-08-29 on a live 5,331-rune memory at snippet_chars=700: 13.2% reported
// against 24.7% actually disclosed. A caller comparing that figure against a
// threshold to decide "do I need a second call?" was deciding on a number that
// under-reported what it already held, which is the defect this whole record is
// about — not the price of a read, but whether a small one can be trusted.
//
// De-duplication is not an optimisation, it is the arithmetic: SnippetRegions
// and SnippetWithHead choose independently and the best-matching region routinely
// falls inside the window, so a naive sum reports coverage ABOVE the truth. On
// that same live memory the naive sum reads 26.8% against a true 24.7% — 108
// runes disclosed once and counted twice. That is the same defect inverted and
// worse, because it reads as completeness.
//
// full is the memory, not its length, because the primary window arrives as
// rendered text and its offsets have to be recovered from it. The task file
// declared `full int`; the signature moved rather than the arithmetic guessing.
func coveredRunes(content string, regions []regionView, full string) float64 {
	total := len([]rune(full))
	if total == 0 {
		return 0
	}
	ranges := primaryRanges(content, full)
	for _, r := range regions {
		n := len([]rune(r.Text))
		if n == 0 || r.Start < 0 {
			continue
		}
		ranges = append(ranges, disclosedRange{r.Start, r.Start + n})
	}
	covered := unionLen(ranges, total)
	c := float64(covered) / float64(total)
	if c > 1 {
		// Kept from the arithmetic it replaces. The union should never exceed the
		// memory now that join markers are excluded by construction — but a clamp
		// that never fires costs nothing, while a removed clamp that should have
		// fired reports coverage above 1.0, which is precisely the over-report the
		// de-duplication above exists to prevent.
		c = 1
	}
	return c
}

// primaryRanges maps the rendered `content` window back to rune ranges in the
// memory it was cut from.
//
// SnippetWithHead has exactly three shapes and this handles all of them without
// asking which one ran: the whole memory (no markers), one contiguous window
// anchored at rune 0 with a trailing "…", and the head joined to a later body by
// " … ". Splitting on the join marker and locating each piece verbatim covers
// all three, because renderSnippet only ever ADDS markers around slices it took
// unchanged.
func primaryRanges(content, full string) []disclosedRange {
	var out []disclosedRange
	for _, piece := range strings.Split(content, snippetJoin) {
		// Only the ellipsis, never surrounding whitespace: a space at the edge of a
		// window IS a rune of the memory that was disclosed, and trimming it
		// under-reports by one rune per edge — small, but in the direction of the
		// defect this function exists to remove.
		piece = strings.Trim(piece, "…")
		if piece == "" {
			continue
		}
		at := strings.Index(full, piece)
		if at < 0 {
			// Not provable as a slice of this memory, so not claimed. Reachable
			// only if a render path starts rewriting text rather than slicing it,
			// which ADR-019 forbids — and the honest failure is a coverage figure
			// that is too LOW, matching what the field meant before this change,
			// rather than one that overstates what the caller received.
			continue
		}
		start := len([]rune(full[:at]))
		out = append(out, disclosedRange{start, start + len([]rune(piece))})
	}
	return out
}

// snippetJoin is the separator SnippetWithHead puts between a memory's opening
// and the later window that matched.
const snippetJoin = " … "

// unionLen is the total length of the given ranges with overlaps counted once,
// bounded to [0, total].
func unionLen(ranges []disclosedRange, total int) int {
	if len(ranges) == 0 {
		return 0
	}
	sort.Slice(ranges, func(a, b int) bool { return ranges[a].start < ranges[b].start })
	sum, cur := 0, ranges[0]
	flush := func(r disclosedRange) {
		if r.start < 0 {
			r.start = 0
		}
		if r.end > total {
			r.end = total
		}
		if r.end > r.start {
			sum += r.end - r.start
		}
	}
	for _, r := range ranges[1:] {
		if r.start <= cur.end {
			if r.end > cur.end {
				cur.end = r.end
			}
			continue
		}
		flush(cur)
		cur = r
	}
	flush(cur)
	return sum
}

// partialWithFetchID marks a view as carrying less than its whole memory.
//
// One marking, so the three sites that set these fields by hand cannot drift
// apart. Both fields together, which drawerView's own comment requires:
// "truncated" without the original length tells a caller something is missing and
// not how much, which is not enough to decide whether to fetch it.
//
// The fetch id is the view's OWN id and is deliberately not a new wire key.
// am_get_drawer(id, whole: true) completes the memory from any chunk's id, so the
// caller already holds the pointer it needs — and ADR-044's primitives audit says
// not to invent a second vocabulary for an idea the response already carries.
// What was missing was never the id; it was the statement that the id is worth
// using.
func partialWithFetchID(v *drawerView, full int) {
	v.Truncated = true
	v.FullLength = full
}
