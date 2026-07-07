package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/synamcps/synamcps-server/internal/models"
)

func TestJSONRPCRequestNotification(t *testing.T) {
	var req JSONRPCRequest
	if err := json.Unmarshal([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !req.IsNotification() {
		t.Fatal("request without id should be notification")
	}
	if req.Params == nil {
		t.Fatal("params should default to an empty map")
	}
}

func TestHandleRequestNotificationReturnsNoResponse(t *testing.T) {
	srv := NewServer(ServerDeps{})
	resp, err := srv.HandleRequest(context.Background(), models.Principal{}, JSONRPCRequest{
		JSONRPC: jsonrpcVersion,
		Method:  "notifications/initialized",
	})
	if err != nil {
		t.Fatalf("HandleRequest: %v", err)
	}
	if resp != nil {
		t.Fatalf("response = %+v, want nil for notification", resp)
	}
}
