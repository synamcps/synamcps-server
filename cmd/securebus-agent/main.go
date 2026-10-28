package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/synamcps/synamcps-server/internal/securebus"
)

// Local stdio MCP sidecar: holds Ed25519 key on disk, signs publish/attest, forwards to SynaMCPs.
func main() {
	baseURL := strings.TrimRight(env("SYNA_BASE_URL", "http://localhost:8080"), "/")
	token := env("SYNA_TOKEN", "")
	keyDir := env("SYNA_SECUREBUS_KEYDIR", filepath.Join(homeDir(), ".synamcps", "securebus"))
	_ = os.MkdirAll(keyDir, 0o700)

	agent := &agent{
		baseURL: baseURL,
		token:   token,
		keyDir:  keyDir,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	enc := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = enc.Encode(rpcError(nil, -32700, "parse error"))
			continue
		}
		resp := agent.handle(req)
		_ = enc.Encode(resp)
	}
}

type agent struct {
	baseURL string
	token   string
	keyDir  string
	client  *http.Client
	keyID   string
	priv    ed25519.PrivateKey
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (a *agent) handle(req rpcRequest) map[string]any {
	switch req.Method {
	case "initialize":
		return rpcResult(req.ID, map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "securebus-agent", "version": "0.1.0"},
		})
	case "notifications/initialized", "initialized":
		return nil
	case "tools/list":
		return rpcResult(req.ID, map[string]any{"tools": a.tools()})
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		out, err := a.callTool(p.Name, p.Arguments)
		if err != nil {
			return rpcResult(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
				"isError": true,
			})
		}
		raw, _ := json.MarshalIndent(out, "", "  ")
		return rpcResult(req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(raw)}},
		})
	default:
		return rpcError(req.ID, -32601, "method not found")
	}
}

func (a *agent) tools() []map[string]any {
	return []map[string]any{
		tool("keygen", "Generate a local Ed25519 keypair under ~/.synamcps/securebus", map[string]any{}),
		tool("register", "Proof-of-possession register public key with SynaMCPs", map[string]any{}),
		tool("securebus_policy", "Fetch signed topic policy", map[string]any{
			"objectId": map[string]any{"type": "string"},
		}, "objectId"),
		tool("securebus_publish", "Sign and publish a message", map[string]any{
			"objectId": map[string]any{"type": "string"},
			"payload":  map[string]any{"type": "object"},
			"nonce":    map[string]any{"type": "string"},
		}, "objectId", "payload"),
		tool("securebus_attest", "Sign and attest ok/deny", map[string]any{
			"objectId":  map[string]any{"type": "string"},
			"messageId": map[string]any{"type": "integer"},
			"status":    map[string]any{"type": "string"},
			"comment":   map[string]any{"type": "string"},
		}, "objectId", "messageId", "status"),
		tool("securebus_verify", "Verify a message", map[string]any{
			"objectId":  map[string]any{"type": "string"},
			"messageId": map[string]any{"type": "integer"},
		}, "objectId", "messageId"),
		tool("securebus_consume", "Consume messages for a condition", map[string]any{
			"objectId":  map[string]any{"type": "string"},
			"consumer":  map[string]any{"type": "string"},
			"condition": map[string]any{"type": "string"},
		}, "objectId"),
		tool("securebus_keys", "List policy public keys", map[string]any{
			"objectId": map[string]any{"type": "string"},
		}, "objectId"),
	}
}

func tool(name, desc string, props map[string]any, required ...string) map[string]any {
	return map[string]any{
		"name":        name,
		"description": desc,
		"inputSchema": map[string]any{"type": "object", "properties": props, "required": required},
	}
}

func (a *agent) callTool(name string, args map[string]any) (any, error) {
	if args == nil {
		args = map[string]any{}
	}
	switch name {
	case "keygen":
		return a.keygen()
	case "register":
		return a.register()
	case "securebus_policy":
		return a.api("GET", "/api/securebus/"+str(args["objectId"])+"/policy", nil)
	case "securebus_verify":
		return a.api("POST", "/api/securebus/"+str(args["objectId"])+"/verify", map[string]any{"messageId": args["messageId"]})
	case "securebus_consume":
		return a.api("POST", "/api/securebus/"+str(args["objectId"])+"/consume", map[string]any{
			"consumer": args["consumer"], "condition": args["condition"],
		})
	case "securebus_keys":
		return a.api("GET", "/api/securebus/"+str(args["objectId"])+"/keys", nil)
	case "securebus_publish":
		return a.publish(str(args["objectId"]), asMap(args["payload"]), str(args["nonce"]))
	case "securebus_attest":
		return a.attest(str(args["objectId"]), int64(asFloat(args["messageId"])), str(args["status"]), str(args["comment"]))
	default:
		return nil, fmt.Errorf("unknown tool %s", name)
	}
}

func (a *agent) keygen() (any, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	id := "local_" + securebus.B64(pub[:8])
	path := filepath.Join(a.keyDir, id+".key")
	if err := os.WriteFile(path, priv, 0o600); err != nil {
		return nil, err
	}
	_ = os.WriteFile(filepath.Join(a.keyDir, "current"), []byte(id), 0o600)
	a.keyID = id
	a.priv = priv
	return map[string]any{
		"localId":   id,
		"publicKey": base64.RawURLEncoding.EncodeToString(pub),
		"path":      path,
	}, nil
}

func (a *agent) loadKey() error {
	if len(a.priv) > 0 {
		return nil
	}
	cur, err := os.ReadFile(filepath.Join(a.keyDir, "current"))
	if err != nil {
		return fmt.Errorf("no key; run keygen first")
	}
	id := strings.TrimSpace(string(cur))
	raw, err := os.ReadFile(filepath.Join(a.keyDir, id+".key"))
	if err != nil {
		return err
	}
	if len(raw) != ed25519.PrivateKeySize {
		return fmt.Errorf("invalid key file")
	}
	a.keyID = id
	a.priv = ed25519.PrivateKey(raw)
	return nil
}

func (a *agent) register() (any, error) {
	if err := a.loadKey(); err != nil {
		return nil, err
	}
	tid := strings.TrimSpace(os.Getenv("SYNA_TOKEN_ID"))
	if tid == "" {
		return nil, fmt.Errorf("set SYNA_TOKEN_ID to the access token id used with SYNA_TOKEN")
	}
	chRaw, err := a.api("POST", "/api/agent-keys/challenge", map[string]any{})
	if err != nil {
		return nil, err
	}
	ch, _ := chRaw.(map[string]any)
	nonce, _ := ch["nonce"].(string)
	pub := a.priv.Public().(ed25519.PublicKey)
	env := map[string]any{
		"nonce": nonce, "publicKey": base64.RawURLEncoding.EncodeToString(pub), "tokenId": tid,
	}
	sig, err := securebus.SignEd25519(a.priv, env)
	if err != nil {
		return nil, err
	}
	reg, err := a.api("POST", "/api/agent-keys", map[string]any{
		"alg": "ed25519", "publicKey": base64.RawURLEncoding.EncodeToString(pub),
		"nonce": nonce, "signature": securebus.B64(sig),
	})
	if err != nil {
		return nil, err
	}
	if m, ok := reg.(map[string]any); ok {
		if kid, _ := m["keyId"].(string); kid != "" {
			_ = os.WriteFile(filepath.Join(a.keyDir, "registered_key_id"), []byte(kid), 0o600)
			a.keyID = kid
		}
	}
	return reg, nil
}

func (a *agent) registeredKeyID() (string, error) {
	if err := a.loadKey(); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(a.keyDir, "registered_key_id"))
	if err != nil {
		return "", fmt.Errorf("run register first")
	}
	return strings.TrimSpace(string(raw)), nil
}

func (a *agent) publish(objectID string, payload map[string]any, nonce string) (any, error) {
	kid, err := a.registeredKeyID()
	if err != nil {
		return nil, err
	}
	polRaw, err := a.api("GET", "/api/securebus/"+objectID+"/policy", nil)
	if err != nil {
		return nil, err
	}
	pol, _ := polRaw.(map[string]any)
	version := asFloat(pol["version"])
	hash, _ := pol["hash"].(string)
	if nonce == "" {
		nonce = fmt.Sprintf("n-%d", time.Now().UnixNano())
	}
	createdAt := time.Now().UTC()
	env := map[string]any{
		"objectId": objectID, "policyVersion": version, "policyHash": hash,
		"authorKeyId": kid, "nonce": nonce, "createdAt": createdAt.Format(time.RFC3339Nano),
		"payload": payload,
	}
	sig, err := securebus.SignEd25519(a.priv, env)
	if err != nil {
		return nil, err
	}
	return a.api("POST", "/api/securebus/"+objectID+"/publish", map[string]any{
		"keyId": kid, "policyVersion": int(version), "policyHash": hash, "nonce": nonce,
		"createdAt": createdAt.Format(time.RFC3339Nano), "payload": payload, "signature": securebus.B64(sig),
	})
}

func (a *agent) attest(objectID string, messageID int64, status, comment string) (any, error) {
	kid, err := a.registeredKeyID()
	if err != nil {
		return nil, err
	}
	polRaw, err := a.api("GET", "/api/securebus/"+objectID+"/policy", nil)
	if err != nil {
		return nil, err
	}
	pol, _ := polRaw.(map[string]any)
	version := asFloat(pol["version"])
	vrRaw, err := a.api("POST", "/api/securebus/"+objectID+"/verify", map[string]any{"messageId": messageID})
	if err != nil {
		return nil, err
	}
	vr, _ := vrRaw.(map[string]any)
	msg, _ := vr["message"].(map[string]any)
	messageHash, _ := msg["hash"].(string)
	createdAt := time.Now().UTC()
	env := map[string]any{
		"messageId": float64(messageID), "messageHash": messageHash, "policyVersion": version,
		"signerKeyId": kid, "status": status, "comment": comment, "createdAt": createdAt.Format(time.RFC3339Nano),
	}
	sig, err := securebus.SignEd25519(a.priv, env)
	if err != nil {
		return nil, err
	}
	return a.api("POST", "/api/securebus/"+objectID+"/attest", map[string]any{
		"keyId": kid, "messageId": messageID, "policyVersion": int(version),
		"status": status, "comment": comment, "createdAt": createdAt.Format(time.RFC3339Nano),
		"signature": securebus.B64(sig),
	})
}

func (a *agent) api(method, path string, body any) (any, error) {
	var rdr io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, a.baseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: %s", method, path, string(raw))
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return string(raw), nil
	}
	return out, nil
}

func rpcResult(id any, result any) map[string]any {
	if id == nil {
		return nil
	}
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

func rpcError(id any, code int, msg string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}}
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	default:
		return 0
	}
}
