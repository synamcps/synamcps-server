package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/synamcps/synamcps-server/internal/access"
	"github.com/synamcps/synamcps-server/internal/bus"
	"github.com/synamcps/synamcps-server/internal/counter"
	"github.com/synamcps/synamcps-server/internal/dataobj"
	"github.com/synamcps/synamcps-server/internal/gitstore"
	"github.com/synamcps/synamcps-server/internal/logstore"
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/policy"
	"github.com/synamcps/synamcps-server/internal/queue"
	"github.com/synamcps/synamcps-server/internal/schedule"
	"github.com/synamcps/synamcps-server/internal/sqltables"
	"github.com/synamcps/synamcps-server/internal/variable"
)

type DataObjectHandler struct {
	Access   *access.Service
	Opener   *policy.Opener
	Variable *variable.Service
	Counter  *counter.Service
	Log      *logstore.Service
	Queue    *queue.Service
	Bus      *bus.Service
	Schedule *schedule.Service
	Table    *sqltables.Service
	Git      *gitstore.Service
}

func (h *DataObjectHandler) Register(mux *http.ServeMux, auth func(http.Handler) http.Handler) {
	mux.Handle("GET /api/objects", auth(http.HandlerFunc(h.listObjects)))
	mux.Handle("POST /api/objects", auth(http.HandlerFunc(h.createObject)))
	mux.Handle("GET /api/objects/{id}", auth(http.HandlerFunc(h.getObject)))
	mux.Handle("PATCH /api/objects/{id}", auth(http.HandlerFunc(h.patchObject)))
	mux.Handle("DELETE /api/objects/{id}", auth(http.HandlerFunc(h.deleteObject)))
	mux.Handle("GET /api/objects/{id}/acl", auth(http.HandlerFunc(h.getACL)))
	mux.Handle("PUT /api/objects/{id}/acl", auth(http.HandlerFunc(h.putACL)))
	mux.Handle("GET /api/schedule/{id}/wait", auth(http.HandlerFunc(h.scheduleWait)))

	for _, kind := range []string{"variable", "counter", "table", "queue", "bus", "schedule", "log", "git", "storage"} {
		mux.Handle("POST /api/"+kind+"/{id}/{method}", auth(http.HandlerFunc(h.invoke)))
		mux.Handle("GET /api/"+kind+"/{id}/{method}", auth(http.HandlerFunc(h.invoke)))
	}
}

// RegisterGit mounts git smart HTTP on the root mux (outside /api/).
func (h *DataObjectHandler) RegisterGit(mux *http.ServeMux, auth func(http.Handler) http.Handler) {
	mux.Handle("POST /git/{id}/{service}", auth(http.HandlerFunc(h.gitSmartHTTP)))
	mux.Handle("GET /git/{id}/info/refs", auth(http.HandlerFunc(h.gitInfoRefs)))
}

func (h *DataObjectHandler) listObjects(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ac := accessContextFromRequest(r)
	kind := models.ObjectKind(r.URL.Query().Get("kind"))
	objs, _, err := h.Access.AvailableObjects(r.Context(), p, ac.AccessToken, ac.AllowedObjects, kind)
	if err != nil {
		http.Error(w, err.Error(), statusFromErr(err))
		return
	}
	writeJSON(w, map[string]any{"objects": objs}, http.StatusOK)
}

func (h *DataObjectHandler) createObject(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Kind    string         `json:"kind"`
		Slug    string         `json:"slug"`
		Name    string         `json:"name"`
		Backend string         `json:"backend"`
		Config  map[string]any `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	obj := models.DataObject{
		Kind:            models.ObjectKind(req.Kind),
		Slug:            req.Slug,
		Name:            req.Name,
		OwnerSubjectKey: models.SubjectKeyForPrincipal(p),
		Backend:         req.Backend,
		Config:          req.Config,
	}
	created, err := h.Access.Store().CreateObject(r.Context(), obj, models.SubjectKeyForPrincipal(p))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, created, http.StatusCreated)
}

func (h *DataObjectHandler) getObject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	obj, ok, err := h.Access.GetObject(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), statusFromErr(err))
		return
	}
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, obj, http.StatusOK)
}

func (h *DataObjectHandler) deleteObject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Access.Store().DeleteObject(r.Context(), id); err != nil {
		http.Error(w, err.Error(), statusFromErr(err))
		return
	}
	writeJSON(w, map[string]string{"status": "deleted"}, http.StatusOK)
}

func (h *DataObjectHandler) patchObject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Config map[string]any `json:"config"`
		Name   string         `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	obj, ok, err := h.Access.GetObject(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), statusFromErr(err))
		return
	}
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	cfg := obj.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	for k, v := range req.Config {
		cfg[k] = v
	}
	updated, err := h.Access.UpdateObjectConfig(r.Context(), id, cfg)
	if err != nil {
		http.Error(w, err.Error(), statusFromErr(err))
		return
	}
	if req.Name != "" && req.Name != updated.Name {
		updated.Name = req.Name
	}
	writeJSON(w, updated, http.StatusOK)
}

func (h *DataObjectHandler) scheduleWait(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ac := accessContextFromRequest(r)
	id := r.PathValue("id")
	claimer := models.SubjectKeyForPrincipal(p)
	if ac.AccessToken != nil {
		claimer = "token:" + ac.AccessToken.ID
	}
	// Authorize via get on the schedule object.
	scope, err := h.Opener.Open(r.Context(), p, ac, id, "get")
	if err != nil {
		http.Error(w, err.Error(), statusFromPolicy(err))
		return
	}
	_ = scope.Commit(r.Context())
	timeoutMs := int(asFloatParam(r.URL.Query().Get("timeoutMs")))
	if timeoutMs <= 0 {
		timeoutMs = 30000
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	d, err := h.Schedule.Wait(ctx, id, claimer, func(c context.Context) (schedule.Delivery, bool, error) {
		if d, ok := h.Schedule.ClaimMemory(id, claimer); ok {
			return d, true, nil
		}
		pollScope, err := h.Opener.Open(c, p, ac, id, "get")
		if err != nil {
			return schedule.Delivery{}, false, err
		}
		return h.Schedule.Claim(c, pollScope, claimer)
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			writeJSON(w, map[string]any{"empty": true}, http.StatusOK)
			return
		}
		http.Error(w, err.Error(), statusFromErr(err))
		return
	}
	writeJSON(w, d, http.StatusOK)
}

func (h *DataObjectHandler) getACL(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	acl, err := h.Access.Store().ObjectACLForObject(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), statusFromErr(err))
		return
	}
	writeJSON(w, map[string]any{"acl": acl}, http.StatusOK)
}

func (h *DataObjectHandler) putACL(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req models.ObjectACLBinding
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.ObjectID = id
	req.GrantedBy = models.SubjectKeyForPrincipal(p)
	b, err := h.Access.Store().UpsertObjectACL(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), statusFromErr(err))
		return
	}
	writeJSON(w, b, http.StatusOK)
}

func (h *DataObjectHandler) invoke(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ac := accessContextFromRequest(r)
	kind := models.ObjectKind(strings.Split(strings.TrimPrefix(r.URL.Path, "/api/"), "/")[0])
	id := r.PathValue("id")
	method := r.PathValue("method")
	if !dataobj.HasMethod(kind, method) {
		http.Error(w, "unknown method", http.StatusBadRequest)
		return
	}
	params := map[string]any{}
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&params)
	} else {
		for k, vals := range r.URL.Query() {
			if len(vals) > 0 {
				params[k] = vals[0]
			}
		}
	}
	result, err := h.dispatch(r.Context(), p, ac, kind, id, method, params, w)
	if err != nil {
		http.Error(w, err.Error(), statusFromPolicy(err))
		return
	}
	if result != nil {
		writeJSON(w, result, http.StatusOK)
	}
}

func (h *DataObjectHandler) dispatch(ctx context.Context, p models.Principal, ac models.APIAccessContext, kind models.ObjectKind, id, method string, params map[string]any, w http.ResponseWriter) (any, error) {
	scope, err := h.Opener.Open(ctx, p, ac, id, method)
	if err != nil {
		return nil, err
	}
	if scope.Kind() != kind {
		_ = scope.Rollback(ctx)
		return nil, policy.ErrKindMismatch
	}
	switch kind {
	case models.ObjectKindVariable:
		name, _ := params["name"].(string)
		prefix, _ := params["prefix"].(string)
		if method == "set" {
			payload, _ := params["payload"].(map[string]any)
			return h.Variable.Set(ctx, scope, name, prefix, payload)
		}
		e, ok, err := h.Variable.Get(ctx, scope, name, prefix)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errNotFound
		}
		return e, nil
	case models.ObjectKindCounter:
		switch method {
		case "set":
			return wrapFloat(h.Counter.Set(ctx, scope, asFloatParam(params["value"])))
		case "get":
			return wrapFloat(h.Counter.Get(ctx, scope))
		case "increase":
			return wrapFloat(h.Counter.Increase(ctx, scope, asFloatParam(params["delta"])))
		case "decrease":
			return wrapFloat(h.Counter.Decrease(ctx, scope, asFloatParam(params["delta"])))
		}
	case models.ObjectKindLog:
		if method == "add" {
			level, _ := params["level"].(string)
			msg, _ := params["message"].(string)
			fields, _ := params["fields"].(map[string]any)
			return h.Log.Add(ctx, scope, level, msg, fields)
		}
		limit := int(asFloatParam(params["limit"]))
		return h.Log.Tail(ctx, scope, limit)
	case models.ObjectKindQueue:
		switch method {
		case "enqueue":
			payload, _ := params["payload"].(map[string]any)
			return h.Queue.Enqueue(ctx, scope, payload)
		case "dequeue":
			m, ok, err := h.Queue.Dequeue(ctx, scope)
			if err != nil {
				return nil, err
			}
			if !ok {
				return map[string]any{"empty": true}, nil
			}
			return m, nil
		case "peek":
			m, ok, err := h.Queue.Peek(ctx, scope)
			if err != nil {
				return nil, err
			}
			if !ok {
				return map[string]any{"empty": true}, nil
			}
			return m, nil
		case "size":
			n, err := h.Queue.Size(ctx, scope)
			return map[string]any{"size": n}, err
		}
	case models.ObjectKindBus:
		switch method {
		case "produce":
			payload, _ := params["payload"].(map[string]any)
			return h.Bus.Produce(ctx, scope, payload)
		case "consume":
			consumer, _ := params["consumer"].(string)
			max := int(asFloatParam(params["max"]))
			return h.Bus.Consume(ctx, scope, consumer, max)
		case "subscribe":
			consumer, _ := params["consumer"].(string)
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			_ = scope.Commit(ctx) // release open tx before long poll
			return nil, h.Bus.Subscribe(ctx, func() (*policy.Scope, error) {
				return h.Opener.Open(ctx, p, ac, id, "consume")
			}, consumer, func(e bus.Event) error {
				raw, _ := json.Marshal(e)
				_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
				if flusher != nil {
					flusher.Flush()
				}
				return nil
			})
		}
	case models.ObjectKindSchedule:
		switch method {
		case "set":
			name, _ := params["name"].(string)
			cronExpr, _ := params["time"].(string)
			if cronExpr == "" {
				cronExpr, _ = params["cron"].(string)
			}
			task, _ := params["task"].(string)
			data, _ := params["data"].(map[string]any)
			return h.Schedule.Set(ctx, scope, name, cronExpr, task, data)
		case "get":
			name, _ := params["name"].(string)
			e, ok, err := h.Schedule.Get(ctx, scope, name)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, errNotFound
			}
			return e, nil
		case "list":
			return h.Schedule.List(ctx, scope)
		}
	case models.ObjectKindTable:
		sqlText, _ := params["sql"].(string)
		return h.Table.Query(ctx, scope, sqlText)
	case models.ObjectKindGit:
		return h.Git.Exec(ctx, scope, method, params)
	case models.ObjectKindStorage:
		_ = scope.Commit(ctx)
		return map[string]any{"hint": "use /api/knowledge for storage RAG methods"}, nil
	}
	_ = scope.Rollback(ctx)
	return nil, policy.ErrUnknownMethod
}

func (h *DataObjectHandler) gitSmartHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ac := accessContextFromRequest(r)
	id := r.PathValue("id")
	service := r.PathValue("service")
	method := "fetch"
	if strings.Contains(service, "receive") {
		method = "push"
	}
	scope, err := h.Opener.Open(r.Context(), p, ac, id, method)
	if err != nil {
		http.Error(w, err.Error(), statusFromPolicy(err))
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	out, ct, err := h.Git.SmartHTTP(r.Context(), scope, service, body)
	if err != nil {
		http.Error(w, err.Error(), statusFromPolicy(err))
		return
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(out)
}

func (h *DataObjectHandler) gitInfoRefs(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ac := accessContextFromRequest(r)
	id := r.PathValue("id")
	service := r.URL.Query().Get("service")
	method := "fetch"
	if strings.Contains(service, "receive") {
		method = "push"
	}
	scope, err := h.Opener.Open(r.Context(), p, ac, id, method)
	if err != nil {
		http.Error(w, err.Error(), statusFromPolicy(err))
		return
	}
	out, ct, err := h.Git.InfoRefs(r.Context(), scope, service)
	if err != nil {
		http.Error(w, err.Error(), statusFromPolicy(err))
		return
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(out)
}

var errNotFound = errors.New("not found")

func statusFromPolicy(err error) int {
	switch {
	case errors.Is(err, policy.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, policy.ErrObjectNotFound), errors.Is(err, errNotFound):
		return http.StatusNotFound
	case errors.Is(err, policy.ErrUnknownMethod), errors.Is(err, policy.ErrKindMismatch):
		return http.StatusBadRequest
	default:
		return http.StatusBadRequest
	}
}

func asFloatParam(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	default:
		return 0
	}
}

func wrapFloat(v float64, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	return map[string]any{"value": v}, nil
}
