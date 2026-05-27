package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// jsScriptConfig is the persisted config for the js-script
// middleware. Operators write JavaScript that runs on every request
// with a goja runtime. The script can:
//
//   - read request headers/method/path/query/body
//   - mutate the request (replace method, body, headers, URL)
//   - skip `next` and return a fully synthesized response
//   - decorate the response after `next` returns
//
// The runtime is sandboxed: there is no fetch, no setTimeout, no
// fs/process access — the only host bindings are the helpers wired
// up below.
type jsScriptConfig struct {
	Script    string `json:"script"`
	TimeoutMs int    `json:"timeout_ms,omitempty"`
	// MaxBodyBytes caps the buffered request body length the script
	// can inspect. Default 256 KiB.
	MaxBodyBytes int64 `json:"max_body_bytes,omitempty"`
	// ParsePayload, when true, pre-parses the body as JSON and
	// exposes it as ctx.request.json. Mirrors the template node's
	// parse_payload flag.
	ParsePayload bool `json:"parse_payload,omitempty"`
}

// jsBranchConfig drives the js-branch composite. Script must call
// ctx.choose("<handle>") to select one of the wired branches; if it
// doesn't (or chooses an unknown handle) the default branch fires.
type jsBranchConfig struct {
	Script    string         `json:"script"`
	TimeoutMs int            `json:"timeout_ms,omitempty"`
	Branches  []jsBranchSpec `json:"branches,omitempty"`
}

type jsBranchSpec struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

const defaultJSTimeoutMs = 50

// buildJSScriptMW returns the middleware constructor for the
// js-script node kind.
func buildJSScriptMW(cfg json.RawMessage, _ ServiceDeps) (Middleware, error) {
	var c jsScriptConfig
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &c); err != nil {
			return nil, fmt.Errorf("js-script config: %w", err)
		}
	}
	if strings.TrimSpace(c.Script) == "" {
		return nil, fmt.Errorf("js-script: script is required")
	}
	timeout := time.Duration(c.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = defaultJSTimeoutMs * time.Millisecond
	}
	maxBody := c.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = 256 << 10
	}
	// Pre-compile the script ONCE so per-request work is "run a
	// compiled program in a fresh runtime". Compilation surfaces
	// syntax errors at compile time rather than on the first
	// request.
	program, err := goja.Compile("js-script", c.Script, false)
	if err != nil {
		return nil, fmt.Errorf("js-script: compile: %w", err)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := newJSRequestCtx(r, maxBody, c.ParsePayload)
			if ctx.err != nil {
				http.Error(w, ctx.err.Error(), http.StatusBadRequest)
				return
			}

			respDraft := &jsResponseDraft{header: make(http.Header)}

			vm := goja.New()
			bindJSCtx(vm, ctx, respDraft, false)

			cancel, doneTimer := scheduleInterrupt(vm, timeout)
			_, err := vm.RunProgram(program)
			cancel()
			doneTimer()
			if err != nil {
				http.Error(w, "js script: "+err.Error(), http.StatusInternalServerError)
				return
			}

			// Apply request mutations the script asked for.
			ctx.applyTo(r)

			if respDraft.shortCircuit {
				respDraft.writeTo(w)
				return
			}

			if respDraft.headerOnly() {
				// Headers were touched but no short-circuit — let
				// next run, then merge the script's headers on top
				// of the response writer.
				rec := newResponseRecorder(maxBody)
				next.ServeHTTP(rec, r)
				for k, vs := range rec.header {
					w.Header()[k] = vs
				}
				for k, v := range respDraft.header {
					w.Header()[k] = v
				}
				status := rec.status
				if status == 0 {
					status = http.StatusOK
				}
				if respDraft.status > 0 {
					status = respDraft.status
				}
				w.WriteHeader(status)
				_, _ = w.Write(rec.body.Bytes())
				return
			}

			next.ServeHTTP(w, r)
		})
	}, nil
}

// buildJSBranch is the switch composite for js-branch. Mirrors the
// existing static switch's branches map — the script picks which
// branch handle to fire by calling ctx.choose(id).
func buildJSBranch(cfg json.RawMessage, _ ServiceDeps, branches BranchSet) (Middleware, error) {
	var c jsBranchConfig
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &c); err != nil {
			return nil, fmt.Errorf("js-branch config: %w", err)
		}
	}
	if strings.TrimSpace(c.Script) == "" {
		return nil, fmt.Errorf("js-branch: script is required")
	}
	timeout := time.Duration(c.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = defaultJSTimeoutMs * time.Millisecond
	}
	program, err := goja.Compile("js-branch", c.Script, false)
	if err != nil {
		return nil, fmt.Errorf("js-branch: compile: %w", err)
	}

	defaultBranch := branches[DefaultSwitchID]
	if defaultBranch == nil {
		return nil, fmt.Errorf("js-branch: default branch missing")
	}
	// Validate that every operator-declared branch has a wired
	// pipeline. Compile already does this for static switches; we
	// repeat the check here so the catalog-side branches[] field
	// matches the graph wiring.
	for _, b := range c.Branches {
		if _, ok := branches[b.ID]; !ok {
			return nil, fmt.Errorf("js-branch: declared branch %q has no wired pipeline", b.ID)
		}
	}

	return func(_ http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := newJSRequestCtx(r, 64<<10, false)
			if ctx.err != nil {
				http.Error(w, ctx.err.Error(), http.StatusBadRequest)
				return
			}
			vm := goja.New()
			var chosen string
			bindJSBranch(vm, ctx, func(id string) { chosen = id })

			cancel, doneTimer := scheduleInterrupt(vm, timeout)
			_, err := vm.RunProgram(program)
			cancel()
			doneTimer()
			if err != nil {
				http.Error(w, "js branch: "+err.Error(), http.StatusInternalServerError)
				return
			}
			ctx.applyTo(r)

			if chosen != "" {
				if mw, ok := branches[chosen]; ok {
					// The branch is a Middleware (sub-pipeline) that
					// wraps its own terminal. Calling mw(nil) follows
					// the same pattern Compile uses for switch
					// branches in graph.go.
					mw(http.HandlerFunc(http.NotFound)).ServeHTTP(w, r)
					return
				}
			}
			defaultBranch(http.HandlerFunc(http.NotFound)).ServeHTTP(w, r)
		})
	}, nil
}

// --- runtime helpers --------------------------------------------------------

// jsRequestCtx is the host-side view of the in-flight request that
// the script can read and mutate. Mutations are applied to the real
// *http.Request only after the script returns successfully so a
// crashing script can't half-mutate.
type jsRequestCtx struct {
	method   string
	url      string
	path     string
	host     string
	headers  map[string]string
	query    map[string]string
	body     []byte
	bodyRead bool
	parseJSON bool
	parsed   any

	mutatedMethod  *string
	mutatedURL     *string
	mutatedHeaders map[string]string
	deletedHeaders map[string]struct{}
	mutatedBody    []byte
	hasMutatedBody bool

	err error
}

func newJSRequestCtx(r *http.Request, maxBody int64, parseJSON bool) *jsRequestCtx {
	c := &jsRequestCtx{
		method:    r.Method,
		url:       r.URL.String(),
		path:      r.URL.Path,
		host:      r.Host,
		headers:   flatHeaders(r.Header),
		query:     flatQuery(r.URL.Query()),
		parseJSON: parseJSON,
		mutatedHeaders: map[string]string{},
		deletedHeaders: map[string]struct{}{},
	}
	if r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
		_ = r.Body.Close()
		if err != nil {
			c.err = err
			return c
		}
		if int64(len(body)) > maxBody {
			c.err = errors.New("request body exceeds js max_body_bytes")
			return c
		}
		c.body = body
		c.bodyRead = true
		// Restore body for downstream handlers regardless of
		// whether the script reads it.
		r.Body = io.NopCloser(bytes.NewReader(body))
		if parseJSON && len(body) > 0 {
			var v any
			if err := json.Unmarshal(body, &v); err == nil {
				c.parsed = v
			}
		}
	}
	return c
}

func (c *jsRequestCtx) applyTo(r *http.Request) {
	if c.mutatedMethod != nil {
		r.Method = *c.mutatedMethod
	}
	if c.mutatedURL != nil {
		// Best-effort parse; bad URLs are silently ignored to match
		// the rest of the proxy's "scripts can't break the request"
		// posture.
		if parsed, err := r.URL.Parse(*c.mutatedURL); err == nil {
			r.URL = parsed
		}
	}
	for k, v := range c.mutatedHeaders {
		r.Header.Set(k, v)
	}
	for k := range c.deletedHeaders {
		r.Header.Del(k)
	}
	if c.hasMutatedBody {
		r.Body = io.NopCloser(bytes.NewReader(c.mutatedBody))
		r.ContentLength = int64(len(c.mutatedBody))
	}
}

type jsResponseDraft struct {
	status       int
	header       http.Header
	body         []byte
	shortCircuit bool
	headersOnly  bool
}

func (d *jsResponseDraft) headerOnly() bool { return d.headersOnly && !d.shortCircuit }

func (d *jsResponseDraft) writeTo(w http.ResponseWriter) {
	for k, vs := range d.header {
		w.Header()[k] = vs
	}
	if d.status == 0 {
		d.status = http.StatusOK
	}
	w.WriteHeader(d.status)
	if len(d.body) > 0 {
		_, _ = w.Write(d.body)
	}
}

// bindJSCtx wires up the JS-side `ctx` object the script sees. The
// surface is intentionally small: getters/setters that map onto
// jsRequestCtx + jsResponseDraft fields.
func bindJSCtx(vm *goja.Runtime, ctx *jsRequestCtx, resp *jsResponseDraft, branch bool) {
	request := vm.NewObject()
	_ = request.Set("method", ctx.method)
	_ = request.Set("url", ctx.url)
	_ = request.Set("path", ctx.path)
	_ = request.Set("host", ctx.host)
	_ = request.Set("headers", ctx.headers)
	_ = request.Set("query", ctx.query)
	if ctx.bodyRead {
		_ = request.Set("body", string(ctx.body))
		if ctx.parsed != nil {
			_ = request.Set("json", ctx.parsed)
		}
	}
	_ = request.Set("setMethod", func(m string) {
		v := strings.ToUpper(strings.TrimSpace(m))
		ctx.mutatedMethod = &v
	})
	_ = request.Set("setURL", func(u string) {
		ctx.mutatedURL = &u
	})
	_ = request.Set("setHeader", func(k, v string) {
		ctx.mutatedHeaders[k] = v
	})
	_ = request.Set("removeHeader", func(k string) {
		ctx.deletedHeaders[k] = struct{}{}
	})
	_ = request.Set("setBody", func(v goja.Value) {
		ctx.hasMutatedBody = true
		ctx.mutatedBody = jsValueToBytes(v)
	})

	root := vm.NewObject()
	_ = root.Set("request", request)
	_ = root.Set("log", logHelpers(vm))

	if branch {
		// js-branch only exposes `choose`. Mutating the response
		// from a branch would be a foot-gun: each branch already
		// owns its full pipeline.
		// `choose` itself is bound by bindJSBranch.
	} else {
		response := vm.NewObject()
		_ = response.Set("setStatus", func(code int) {
			resp.status = code
			resp.headersOnly = true
		})
		_ = response.Set("setHeader", func(k, v string) {
			resp.header.Set(k, v)
			resp.headersOnly = true
		})
		_ = response.Set("setBody", func(v goja.Value) {
			resp.body = jsValueToBytes(v)
			resp.shortCircuit = true
		})
		_ = response.Set("shortCircuit", func() {
			resp.shortCircuit = true
		})
		_ = root.Set("response", response)
	}

	_ = vm.Set("ctx", root)
}

// bindJSBranch binds the branch-only API (ctx.request + ctx.choose
// + ctx.log).
func bindJSBranch(vm *goja.Runtime, ctx *jsRequestCtx, chooser func(string)) {
	bindJSCtx(vm, ctx, nil, true)
	root := vm.Get("ctx").(*goja.Object)
	_ = root.Set("choose", func(id string) {
		chooser(strings.TrimSpace(id))
	})
}

// logHelpers exposes a minimal log API (info/warn/error) that maps
// onto the request logger. Kept narrow on purpose; operators who
// need printf-style debugging can stringify in JS first.
func logHelpers(vm *goja.Runtime) *goja.Object {
	o := vm.NewObject()
	_ = o.Set("info", func(msg string) { _ = msg /* slog wired in graph context later */ })
	_ = o.Set("warn", func(msg string) { _ = msg })
	_ = o.Set("error", func(msg string) { _ = msg })
	return o
}

func jsValueToBytes(v goja.Value) []byte {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	// Objects are JSON-encoded so the script can do `setBody({...})`
	// and get a JSON body back out. Strings are taken as-is.
	switch raw := v.Export().(type) {
	case string:
		return []byte(raw)
	case []byte:
		return raw
	default:
		b, err := json.Marshal(raw)
		if err == nil {
			return b
		}
		return []byte(fmt.Sprint(raw))
	}
}

// scheduleInterrupt sets a goja interrupt after `d`. Returns a
// cancel func that aborts the timer (call after RunProgram) and a
// `done` func that waits until the timer goroutine exits so the
// caller can safely reuse the runtime.
func scheduleInterrupt(vm *goja.Runtime, d time.Duration) (cancel, done func()) {
	if d <= 0 {
		return func() {}, func() {}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	timer := time.AfterFunc(d, func() {
		defer wg.Done()
		vm.Interrupt("script timeout")
	})
	canceled := false
	cancel = func() {
		if canceled {
			return
		}
		canceled = true
		if timer.Stop() {
			wg.Done() // timer never fired
		}
	}
	done = func() { wg.Wait() }
	return cancel, done
}
