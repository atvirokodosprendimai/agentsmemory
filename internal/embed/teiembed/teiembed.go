// Package teiembed embeds text with a HuggingFace text-embeddings-inference
// (TEI) server. It is an alternative to internal/embed/ollama, selected with
// EMBED_BACKEND=tei; Ollama stays the default so existing deployments are
// unaffected by this package existing.
//
// Why a second embedder: TEI is already the server that runs the re-ranking
// cross-encoder (internal/rerank/tei), so a deployment with a GPU box can embed
// and rerank there and stop keeping an Ollama alive purely for /api/embed. The
// wire shape is also plainer — one POST, a bare JSON array back.
//
// The package is named teiembed rather than tei so it does not collide with
// internal/rerank/tei at the composition root, following the same naming choice
// made for internal/store/chromemvec.
//
// WHAT TEI CANNOT DO HERE, recorded so nobody re-derives it. bge-m3's sparse
// (lexical_weights) head is unreachable through TEI. /embed_sparse requires
// SPLADE pooling — sparse weights read off an MLM head as logits over the whole
// vocabulary — whereas bge-m3 produces its sparse weights from a separate
// trained sparse_linear layer that TEI does not load. Probed 2026-08-19 against
// a live BAAI/bge-m3 (TEI 1.9.3): /embed_sparse answers
//
//	424 {"error":"Backend error: Model is not an embedding model with SPLADE pooling"}
//
// Upstream PR #899 (--pooling m3_sparse) would add it but is unmerged. Note also
// that TEI's --pooling is process-wide, so one container cannot serve dense and
// sparse for the same model even once that lands.
package teiembed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"
)

// maxBatch is the safe fallback when TEI's /info capability endpoint is not
// reachable. It mirrors TEI's --max-client-batch-size default, which rejects a
// larger array outright with a 422 rather than truncating it.
//
// A deployment may advertise a larger limit through /info. The client uses it
// up to maxDiscoveredBatch so the production 128-input server needs one request
// instead of four while payloads remain bounded. Splitting is exact here: each
// input is embedded independently, so grouping cannot change a vector.
const maxBatch = 32

const maxDiscoveredBatch = 128

// infoTimeout bounds the capability probe. It is short because /info is a
// static read served before any inference, and because the probe is on the
// critical path of the embed that triggered it.
const infoTimeout = 5 * time.Second

// defaultProbeRetry is how long a failed capability probe is left alone. Long
// enough that a permanently /info-less deployment costs one request a minute
// rather than one per embed, short enough that a TEI still loading its model is
// picked up well within a warm-up.
const defaultProbeRetry = 30 * time.Second

// Embedder is a client for TEI's /embed endpoint. It satisfies the Embedder
// interface that internal/palace declares at the consumer.
type Embedder struct {
	endpoint     string
	infoEndpoint string
	http         *http.Client

	// retryAfter is how long a failed probe suppresses the next one. Unexported
	// and injectable so a test can drive the policy without sleeping; it is not
	// an operator knob.
	retryAfter time.Duration

	// batchMu guards the fields below. It is a mutex and not a sync.Once because
	// Once fires whether the probe succeeded or FAILED, which pinned a transient
	// error to the whole process lifetime. The mutex is NEVER held across the
	// probe itself — see clientBatchSize.
	batchMu   sync.Mutex
	batchSize int
	// inputWindow is the model's max input length in TOKENS, as reported by
	// /info. Zero until a probe succeeds, and zero forever on a server that does
	// not report it — which is why it is only ever put on a span when positive.
	//
	// The same /info response already carried this and the decoder ignored it, so
	// the number every chunk-size decision in this repository wants existed one
	// struct field away for as long as the probe has run.
	inputWindow int
	// model is the model id /info reports, for the same reason: a distance is
	// uninterpretable without it, and two 1024-dimension models are two different
	// embedding spaces.
	model       string
	probing     bool
	nextProbe   time.Time
	batchWarned bool

	// safeEndpoint is infoEndpoint with any userinfo stripped, for logs.
	//
	// EMBED_URL is an operator-supplied URL and http://user:pass@host is a valid
	// one, so logging the configured address verbatim writes the password into
	// whatever collects those lines — where it outlives the process, is readable
	// by anyone with log access, and is invisible to the operator who set it.
	// Computed once here rather than at each log site so a future log line cannot
	// reach for the wrong field by habit.
	safeEndpoint string
}

// maxErrorBody bounds how much of an upstream response body is carried into an
// error. Enough for TEI's {"error":…,"error_type":…} — the only thing that
// distinguishes "this model cannot do that" (424) from "batch too large" (422) —
// and not enough to relay a document.
const maxErrorBody = 256

// boundedBody trims an upstream response body for inclusion in an error.
//
// The body is data from ANOTHER server, and this error is logged by the caller
// (palace's embedOrDefer warns with it). ADR-024 is explicit that logs must not
// carry passage text, and the passages are exactly what was just sent upstream —
// so a compromised or merely chatty embed server echoing its input would put
// memory content into our logs through a path nobody would think to audit.
//
// Bounding does not make that impossible, and is not claimed to: it makes the
// exposure a fixed, small size instead of "however much the other end chose to
// send", which is the difference between a leak and a whole document.
func boundedBody(data []byte) string {
	body := strings.TrimSpace(string(data))
	if r := []rune(body); len(r) > maxErrorBody {
		return string(r[:maxErrorBody]) + "… (truncated)"
	}
	return body
}

// redactURL removes userinfo from a URL for logging, returning it otherwise
// unchanged so the host, port and path stay diagnosable. A redaction that also
// hid the host would turn a connectivity bug into an unreadable log line, which
// is how redaction gets removed again.
//
// It clears User outright rather than using url.Redacted(), which masks only the
// PASSWORD and prints the username verbatim. A username identifies an account
// and contributes nothing to "which endpoint failed", so there is no reason to
// keep it in a line that may be shipped to a log collector.
//
// A URL that does not parse is reported as an opaque placeholder rather than
// echoed: an unparseable value is exactly the case where assuming it holds no
// secret is unjustified.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(unparseable embed URL)"
	}
	u.User = nil
	return u.String()
}

// New constructs an Embedder for the given TEI base URL (e.g.
// http://host:13434). A trailing slash on baseURL is tolerated. timeout bounds
// each batched call.
//
// Nothing here names a model: TEI serves exactly the one fixed by the
// container's --model-id, so a model field would be a knob the wire cannot
// carry. This is the same reason internal/config has no RERANK_MODEL.
func New(baseURL string, timeout time.Duration) *Embedder {
	baseURL = strings.TrimRight(baseURL, "/")
	return &Embedder{
		endpoint:     baseURL + "/embed",
		infoEndpoint: baseURL + "/info",
		safeEndpoint: redactURL(baseURL) + "/info",
		http:         telemetry.HTTPClient(timeout),
		retryAfter:   defaultProbeRetry,
	}
}

// embedRequest is TEI's embed payload. Truncate is set so an input longer than
// the model's context yields a (shortened) vector instead of a 413 that would
// fail a whole batch.
//
// ⚠It is a LAST resort, not a reason to relax about input size, and this comment
// used to say otherwise. It claimed truncation "should never actually trigger"
// because chunking bounds our inputs below bge-m3's 8192 tokens — true of the add
// path, which chunks at 1600 characters, and false of the update path as it stood
// then, which re-embedded a whole memory with EmbedOne and never chunked it.
// Nothing on that path was bounded, so an oversized update got a prefix vector and
// a 200, and the tail of the memory became unfindable while still reading back
// whole.
//
// What bounds it now is the WRITE PATH, not a check in front of this request:
// since ADR-038 T4 a content change is a supersede that files the new text
// through Add, so a memory is embedded chunk by chunk whichever call stores it,
// at most palace.ChunkSize characters in one piece.
//
// ⚠An earlier version of this paragraph named a caller-side constant as a guard
// that stopped an oversized input before the request was built. That check was
// deleted along with the unbounded update path it protected, and the sentence
// outlived it by ten days. Half a comment that is still true is the dangerous
// kind — the paragraph below was checked, found correct, and lent its credibility
// to a sentence pointing at code nobody runs.
//
// ⚠Truncation here is therefore still REACHABLE, and not only through a bug:
// palace.CheckDuplicate embeds caller-supplied content through EmbedOne with no
// bound of its own, because a wrong duplicate verdict is a read-only answer and
// stores no vector. So treat this flag as the batch's protection against inputs
// nobody bounded — not as proof that none exist.
type embedRequest struct {
	Inputs   []string `json:"inputs"`
	Truncate bool     `json:"truncate"`
}

// Embed returns one vector per input string, in order. An empty input slice
// short-circuits to nil so callers need not special-case it.
//
// Inputs beyond the server's discovered client limit are split across several
// requests and reassembled in the caller's order.
func (e *Embedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	batchSize := len(inputs)
	if len(inputs) > 1 {
		batchSize = e.clientBatchSize(ctx)
	}
	out := make([][]float32, 0, len(inputs))
	for start := 0; start < len(inputs); start += batchSize {
		end := min(start+batchSize, len(inputs))
		batch, err := e.embedBatch(ctx, inputs[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

// clientBatchSize reports the server's real client limit, discovering it on
// first use and CACHING ONLY A SUCCESSFUL ANSWER. Capability discovery is an
// optimisation rather than an availability dependency: a proxy may expose
// /embed without /info, in which case the public TEI default is the safe
// answer — but it must be a safe answer for this call, not a verdict on the
// process.
//
// Memoizing failure is how the optimisation disappears in production. TEI is
// commonly still loading its model when the first embed arrives, and one 503,
// one dial error, or one caller who hangs up is otherwise enough to pin 32 for
// the lifetime of the server, four times the round trips, with nothing said.
func (e *Embedder) clientBatchSize(ctx context.Context) int {
	e.batchMu.Lock()
	if e.batchSize > 0 {
		size := e.batchSize
		e.batchMu.Unlock()
		return size
	}
	// Do not queue behind someone else's probe, and do not re-ask a server that
	// just refused. Either way the safe default is the right answer for THIS
	// call: discovery is an optimisation, so waiting for it is always worse than
	// proceeding without it.
	if e.probing || time.Now().Before(e.nextProbe) {
		e.batchMu.Unlock()
		return maxBatch
	}
	e.probing = true
	e.batchMu.Unlock()

	// Deliberately OUTSIDE the lock. Holding it across a network call made every
	// embed in the process wait on one /info, so a proxy that black-holes the
	// path turned the whole write path — add_drawer chunking, mining, the
	// backfill worker, every tenant — into a queue one timeout wide.
	size, err := e.discoverClientBatchSize(ctx)

	e.batchMu.Lock()
	e.probing = false
	if err != nil {
		e.nextProbe = time.Now().Add(e.retryAfter)
		warn := !e.batchWarned
		e.batchWarned = true
		e.batchMu.Unlock()
		// Said once, not per call: a warning on every embed would bury the log
		// of a server whose /info is permanently absent, which is a supported
		// deployment rather than a fault.
		if warn {
			slog.Warn("TEI capability discovery failed; using the default client batch size and will retry",
				"endpoint", e.safeEndpoint, "batch", maxBatch, "error", err)
		}
		return maxBatch
	}
	e.batchSize = size
	e.batchMu.Unlock()
	return size
}

// discoverClientBatchSize asks TEI what it will accept.
//
// The probe deliberately does NOT inherit the caller's cancellation. Whichever
// embed happens to be first would otherwise decide a process-wide setting, so a
// client that disconnects mid-request downgrades every later batch. It keeps
// the caller's values (deadline aside) so tracing and auth survive.
func (e *Embedder) discoverClientBatchSize(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), infoTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.infoEndpoint, nil)
	if err != nil {
		return 0, err
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("GET %s: %s", e.safeEndpoint, resp.Status)
	}
	var info struct {
		MaxClientBatchSize int    `json:"max_client_batch_size"`
		MaxInputLength     int    `json:"max_input_length"`
		ModelID            string `json:"model_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return 0, fmt.Errorf("decode %s: %w", e.safeEndpoint, err)
	}
	if info.MaxClientBatchSize < 1 {
		return 0, fmt.Errorf("%s advertised max_client_batch_size=%d", e.safeEndpoint, info.MaxClientBatchSize)
	}
	// Keep what the SAME response already told us. These are recorded rather than
	// acted on: nothing sizes a chunk from them yet, and the first step to sizing
	// anything from a model's real window is being able to state it at all.
	e.batchMu.Lock()
	e.inputWindow, e.model = info.MaxInputLength, info.ModelID
	e.batchMu.Unlock()
	return min(info.MaxClientBatchSize, maxDiscoveredBatch), nil
}

// embedBatch embeds one request's worth of inputs and returns the vectors in
// request order. Its caller has already bounded the slice to the server limit.
func (e *Embedder) embedBatch(ctx context.Context, inputs []string) ([][]float32, error) {
	raw, err := json.Marshal(embedRequest{Inputs: inputs, Truncate: true})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// TEI puts the reason in the body ({"error":...,"error_type":...}) and it is
		// the only thing that distinguishes "this model cannot do that" (424) from
		// "batch too large" (422), so it is worth carrying into the error.
		return nil, fmt.Errorf("tei: embed -> %d: %s", resp.StatusCode, boundedBody(data))
	}

	// TEI answers with a BARE array of vectors, not an object with a field —
	// unlike Ollama's {"embeddings":[...]}.
	var vectors [][]float32
	if err := json.Unmarshal(data, &vectors); err != nil {
		return nil, fmt.Errorf("tei: decode embed response: %w", err)
	}
	// Guard the invariant the rest of the system relies on: one vector per input.
	if len(vectors) != len(inputs) {
		return nil, fmt.Errorf("tei: expected %d embeddings, got %d", len(inputs), len(vectors))
	}
	return vectors, nil
}

// EmbedOne is a convenience for the common single-string case (e.g. a search
// query), returning just that one vector.
func (e *Embedder) EmbedOne(ctx context.Context, input string) ([]float32, error) {
	vecs, err := e.Embed(ctx, []string{input})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// DescribeEmbedder names this backend, its model and the model's input window
// for a span.
//
// ⚠THE PROBE ONLY FIRES ON A MULTI-INPUT BATCH. Embed consults clientBatchSize
// only when len(inputs) > 1, so EmbedOne — which is the ONLY call a search makes
// — never triggers discovery. On a server that is read-mostly the window and
// model therefore stay unknown until some write embeds a batch, and the span
// omits them until then. That is stated rather than fixed: making a search wait
// on /info would put a network call on the recall path, which this file already
// records as having turned the whole write path into a queue one timeout wide.
//
// The model and window are whatever the LAST successful /info probe reported,
// and both are empty/zero until one succeeds — this reports what was measured,
// never what a comment claims. That distinction is the point: before this, every
// 8192 in the repository was prose, and ChunkSize sat at 5% of it on that
// authority alone.
func (e *Embedder) DescribeEmbedder() (backend, model string, windowTokens int) {
	e.batchMu.Lock()
	defer e.batchMu.Unlock()
	return "tei", e.model, e.inputWindow
}
