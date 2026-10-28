package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/synamcps/synamcps-server/internal/bus"
	"github.com/synamcps/synamcps-server/internal/counter"
	"github.com/synamcps/synamcps-server/internal/dataobj"
	"github.com/synamcps/synamcps-server/internal/gitstore"
	"github.com/synamcps/synamcps-server/internal/logstore"
	"github.com/synamcps/synamcps-server/internal/mcpdesc"
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/policy"
	"github.com/synamcps/synamcps-server/internal/queue"
	"github.com/synamcps/synamcps-server/internal/schedule"
	"github.com/synamcps/synamcps-server/internal/securebus"
	"github.com/synamcps/synamcps-server/internal/sqltables"
	"github.com/synamcps/synamcps-server/internal/strutil"
	"github.com/synamcps/synamcps-server/internal/variable"
)

type DataServices struct {
	Opener    *policy.Opener
	Variable  *variable.Service
	Counter   *counter.Service
	Log       *logstore.Service
	Queue     *queue.Service
	Bus       *bus.Service
	SecureBus *securebus.Service
	Schedule  *schedule.Service
	Table     *sqltables.Service
	Git       *gitstore.Service
}

func (s *Server) AttachDataServices(ds DataServices) {
	s.data = ds
}

func (s *Server) buildDataObjectTools(ctx context.Context, p models.Principal, accessCtx models.APIAccessContext) ([]map[string]any, error) {
	if s.access == nil || s.data.Opener == nil {
		return nil, nil
	}
	out := []map[string]any{}
	kinds := []models.ObjectKind{
		models.ObjectKindVariable, models.ObjectKindCounter, models.ObjectKindTable,
		models.ObjectKindQueue, models.ObjectKindBus, models.ObjectKindSecureBus, models.ObjectKindSchedule,
		models.ObjectKindLog, models.ObjectKindGit,
	}
	for _, kind := range kinds {
		objs, _, err := s.access.AvailableObjects(ctx, p, accessCtx.AccessToken, accessCtx.AllowedObjects, kind)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(objs))
		for _, o := range objs {
			ids = append(ids, o.ID)
		}
		if len(ids) == 0 {
			continue
		}
		for _, method := range dataobj.Methods(kind) {
			name := string(kind) + "_" + method
			if !dataToolAllowed(name, accessCtx.AllowedObjects) {
				continue
			}
			props := map[string]any{
				"objectId": map[string]any{"type": "string", "enum": ids, "description": "Data object ID"},
			}
			required := []string{"objectId"}
			switch kind {
			case models.ObjectKindVariable:
				props["name"] = map[string]any{"type": "string"}
				props["prefix"] = map[string]any{"type": "string"}
				if method == "set" {
					props["payload"] = map[string]any{"type": "object"}
					required = append(required, "name", "payload")
				} else {
					required = append(required, "name")
				}
			case models.ObjectKindCounter:
				if method == "set" {
					props["value"] = map[string]any{"type": "number"}
					required = append(required, "value")
				}
				if method == "increase" || method == "decrease" {
					props["delta"] = map[string]any{"type": "number"}
				}
			case models.ObjectKindLog:
				if method == "add" {
					props["message"] = map[string]any{"type": "string"}
					props["level"] = map[string]any{"type": "string"}
					props["fields"] = map[string]any{"type": "object"}
					required = append(required, "message")
				} else {
					props["limit"] = map[string]any{"type": "integer"}
				}
			case models.ObjectKindQueue:
				if method == "enqueue" {
					props["payload"] = map[string]any{"type": "object"}
					required = append(required, "payload")
				}
			case models.ObjectKindBus:
				if method == "produce" {
					props["payload"] = map[string]any{"type": "object"}
					required = append(required, "payload")
				}
				if method == "consume" || method == "subscribe" {
					props["consumer"] = map[string]any{"type": "string"}
				}
			case models.ObjectKindSecureBus:
				switch method {
				case "publish":
					props["keyId"] = map[string]any{"type": "string"}
					props["policyVersion"] = map[string]any{"type": "integer"}
					props["policyHash"] = map[string]any{"type": "string"}
					props["nonce"] = map[string]any{"type": "string"}
					props["createdAt"] = map[string]any{"type": "string"}
					props["payload"] = map[string]any{"type": "object"}
					props["signature"] = map[string]any{"type": "string"}
					props["prevHash"] = map[string]any{"type": "string"}
					required = append(required, "keyId", "policyVersion", "policyHash", "nonce", "signature", "payload")
				case "attest":
					props["keyId"] = map[string]any{"type": "string"}
					props["messageId"] = map[string]any{"type": "integer"}
					props["policyVersion"] = map[string]any{"type": "integer"}
					props["status"] = map[string]any{"type": "string", "enum": []any{"ok", "deny"}}
					props["comment"] = map[string]any{"type": "string"}
					props["createdAt"] = map[string]any{"type": "string"}
					props["signature"] = map[string]any{"type": "string"}
					required = append(required, "keyId", "messageId", "policyVersion", "status", "signature")
				case "verify":
					props["messageId"] = map[string]any{"type": "integer"}
					required = append(required, "messageId")
				case "consume", "subscribe":
					props["consumer"] = map[string]any{"type": "string"}
					props["condition"] = map[string]any{"type": "string"}
					props["filter"] = map[string]any{"type": "object"}
				case "policy":
					props["version"] = map[string]any{"type": "integer"}
				}
			case models.ObjectKindSchedule:
				if method == "set" {
					props["name"] = map[string]any{"type": "string"}
					props["time"] = map[string]any{"type": "string", "description": "cron or @every duration"}
					props["task"] = map[string]any{"type": "string"}
					props["data"] = map[string]any{"type": "object"}
					required = append(required, "name", "time", "task")
				}
				if method == "get" {
					props["name"] = map[string]any{"type": "string"}
					required = append(required, "name")
				}
			case models.ObjectKindTable:
				props["sql"] = map[string]any{"type": "string"}
				required = append(required, "sql")
			case models.ObjectKindGit:
				props["args"] = map[string]any{"type": "object"}
			}
			out = append(out, mcpdesc.Tool(name, fmt.Sprintf("%s.%s on a %s object", kind, method, kind), map[string]any{
				"type":       "object",
				"properties": props,
				"required":   required,
			}))
		}
	}
	return out, nil
}

func dataToolAllowed(name string, scopes []models.AccessTokenObject) bool {
	if len(scopes) == 0 {
		return true
	}
	for _, scope := range scopes {
		if len(scope.MethodAllowlist) == 0 {
			return true
		}
		for _, allowed := range scope.MethodAllowlist {
			if allowed == name {
				return true
			}
		}
	}
	return false
}

func (s *Server) handleDataObjectTool(ctx context.Context, p models.Principal, accessCtx models.APIAccessContext, toolName string, params map[string]any) (any, error) {
	if s.data.Opener == nil {
		return nil, fmt.Errorf("data object services not configured")
	}
	kind, method, ok := splitKindMethod(toolName)
	if !ok || !dataobj.HasMethod(kind, method) {
		return nil, fmt.Errorf("unknown data tool")
	}
	objectID := strutil.AsString(params["objectId"])
	scope, err := s.data.Opener.Open(ctx, p, accessCtx, objectID, method)
	if err != nil {
		return nil, err
	}
	switch kind {
	case models.ObjectKindVariable:
		name := strutil.AsString(params["name"])
		prefix := strutil.AsString(params["prefix"])
		if method == "set" {
			payload, _ := params["payload"].(map[string]any)
			return s.data.Variable.Set(ctx, scope, name, prefix, payload)
		}
		e, found, err := s.data.Variable.Get(ctx, scope, name, prefix)
		if err != nil {
			return nil, err
		}
		if !found {
			return map[string]any{"found": false}, nil
		}
		return e, nil
	case models.ObjectKindCounter:
		switch method {
		case "set":
			v, err := s.data.Counter.Set(ctx, scope, asFloat(params["value"]))
			return map[string]any{"value": v}, err
		case "get":
			v, err := s.data.Counter.Get(ctx, scope)
			return map[string]any{"value": v}, err
		case "increase":
			v, err := s.data.Counter.Increase(ctx, scope, asFloatOr(params["delta"], 1))
			return map[string]any{"value": v}, err
		case "decrease":
			v, err := s.data.Counter.Decrease(ctx, scope, asFloatOr(params["delta"], 1))
			return map[string]any{"value": v}, err
		}
	case models.ObjectKindLog:
		if method == "add" {
			fields, _ := params["fields"].(map[string]any)
			return s.data.Log.Add(ctx, scope, strutil.AsString(params["level"]), strutil.AsString(params["message"]), fields)
		}
		return s.data.Log.Tail(ctx, scope, int(asFloatOr(params["limit"], 100)))
	case models.ObjectKindQueue:
		switch method {
		case "enqueue":
			payload, _ := params["payload"].(map[string]any)
			return s.data.Queue.Enqueue(ctx, scope, payload)
		case "dequeue":
			m, ok, err := s.data.Queue.Dequeue(ctx, scope)
			if err != nil {
				return nil, err
			}
			if !ok {
				return map[string]any{"empty": true}, nil
			}
			return m, nil
		case "peek":
			m, ok, err := s.data.Queue.Peek(ctx, scope)
			if err != nil {
				return nil, err
			}
			if !ok {
				return map[string]any{"empty": true}, nil
			}
			return m, nil
		case "size":
			n, err := s.data.Queue.Size(ctx, scope)
			return map[string]any{"size": n}, err
		}
	case models.ObjectKindBus:
		switch method {
		case "produce":
			payload, _ := params["payload"].(map[string]any)
			return s.data.Bus.Produce(ctx, scope, payload)
		case "consume", "subscribe":
			return s.data.Bus.Consume(ctx, scope, strutil.AsString(params["consumer"]), int(asFloatOr(params["max"], 10)))
		}
	case models.ObjectKindSecureBus:
		return s.handleSecureBusTool(ctx, p, accessCtx, scope, method, params)
	case models.ObjectKindSchedule:
		switch method {
		case "set":
			data, _ := params["data"].(map[string]any)
			cronExpr := strutil.AsString(params["time"])
			if cronExpr == "" {
				cronExpr = strutil.AsString(params["cron"])
			}
			return s.data.Schedule.Set(ctx, scope, strutil.AsString(params["name"]), cronExpr, strutil.AsString(params["task"]), data)
		case "get":
			e, found, err := s.data.Schedule.Get(ctx, scope, strutil.AsString(params["name"]))
			if err != nil {
				return nil, err
			}
			if !found {
				return map[string]any{"found": false}, nil
			}
			return e, nil
		case "list":
			return s.data.Schedule.List(ctx, scope)
		}
	case models.ObjectKindTable:
		return s.data.Table.Query(ctx, scope, strutil.AsString(params["sql"]))
	case models.ObjectKindGit:
		args, _ := params["args"].(map[string]any)
		if args == nil {
			args = params
		}
		return s.data.Git.Exec(ctx, scope, method, args)
	}
	_ = scope.Rollback(ctx)
	return nil, policy.ErrUnknownMethod
}

func (s *Server) handleSecureBusTool(ctx context.Context, p models.Principal, accessCtx models.APIAccessContext, scope *policy.Scope, method string, params map[string]any) (any, error) {
	if s.data.SecureBus == nil {
		_ = scope.Rollback(ctx)
		return nil, fmt.Errorf("securebus not configured")
	}
	tokenID := ""
	if accessCtx.AccessToken != nil {
		tokenID = accessCtx.AccessToken.ID
	}
	subject := models.SubjectKeyForPrincipal(p)
	switch method {
	case "policy":
		return s.data.SecureBus.GetPolicy(ctx, scope, int(asFloatOr(params["version"], 0)))
	case "publish":
		payload, _ := params["payload"].(map[string]any)
		createdAt, _ := parseTime(strutil.AsString(params["createdAt"]))
		return s.data.SecureBus.Publish(ctx, scope, securebus.PublishInput{
			KeyID: strutil.AsString(params["keyId"]), SubjectKey: subject, TokenID: tokenID,
			PolicyVersion: int(asFloat(params["policyVersion"])), PolicyHash: strutil.AsString(params["policyHash"]),
			Nonce: strutil.AsString(params["nonce"]), CreatedAt: createdAt, Payload: payload,
			Signature: strutil.AsString(params["signature"]), PrevHash: strutil.AsString(params["prevHash"]),
		})
	case "attest":
		createdAt, _ := parseTime(strutil.AsString(params["createdAt"]))
		return s.data.SecureBus.Attest(ctx, scope, securebus.AttestInput{
			KeyID: strutil.AsString(params["keyId"]), SubjectKey: subject, TokenID: tokenID,
			MessageID: int64(asFloat(params["messageId"])), PolicyVersion: int(asFloat(params["policyVersion"])),
			Status: strutil.AsString(params["status"]), Comment: strutil.AsString(params["comment"]),
			CreatedAt: createdAt, Signature: strutil.AsString(params["signature"]),
		})
	case "verify":
		return s.data.SecureBus.Verify(ctx, scope, int64(asFloat(params["messageId"])))
	case "keys":
		return s.data.SecureBus.Keys(ctx, scope)
	case "consume", "subscribe":
		filter, _ := params["filter"].(map[string]any)
		return s.data.SecureBus.Consume(ctx, scope, strutil.AsString(params["consumer"]), strutil.AsString(params["condition"]), filter, int(asFloatOr(params["max"], 10)))
	}
	_ = scope.Rollback(ctx)
	return nil, policy.ErrUnknownMethod
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, s)
}

func splitKindMethod(toolName string) (models.ObjectKind, string, bool) {
	for _, kind := range []models.ObjectKind{
		models.ObjectKindVariable, models.ObjectKindCounter, models.ObjectKindTable,
		models.ObjectKindQueue, models.ObjectKindBus, models.ObjectKindSecureBus, models.ObjectKindSchedule,
		models.ObjectKindLog, models.ObjectKindGit,
	} {
		prefix := string(kind) + "_"
		if len(toolName) > len(prefix) && toolName[:len(prefix)] == prefix {
			return kind, toolName[len(prefix):], true
		}
	}
	return "", "", false
}

func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	default:
		return 0
	}
}

func asFloatOr(v any, def float64) float64 {
	if v == nil {
		return def
	}
	f := asFloat(v)
	if f == 0 {
		return def
	}
	return f
}
