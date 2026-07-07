package mcp

import (
	"encoding/json"
)

const jsonrpcVersion = "2.0"

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  map[string]any  `json:"params,omitempty"`
}

func (r *JSONRPCRequest) UnmarshalJSON(data []byte) error {
	type wireRequest struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	var wire wireRequest
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	r.JSONRPC = wire.JSONRPC
	r.ID = wire.ID
	r.Method = wire.Method
	r.Params = map[string]any{}
	if len(wire.Params) == 0 || string(wire.Params) == "null" {
		return nil
	}
	if err := json.Unmarshal(wire.Params, &r.Params); err != nil {
		return err
	}
	return nil
}

func (r JSONRPCRequest) IsNotification() bool {
	return len(r.ID) == 0
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func NewResultResponse(id json.RawMessage, result any) JSONRPCResponse {
	return JSONRPCResponse{JSONRPC: jsonrpcVersion, ID: id, Result: result}
}

func NewErrorResponse(id json.RawMessage, code int, message string) JSONRPCResponse {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return JSONRPCResponse{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Error:   &JSONRPCError{Code: code, Message: message},
	}
}
