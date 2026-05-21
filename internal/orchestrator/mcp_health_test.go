package orchestrator

import (
	"encoding/json"
	"testing"
	"time"
)

func TestServer_pingTool(t *testing.T) {
	town := t.TempDir()
	mgr := NewManager(town)
	srv := NewServer(mgr)

	params, _ := json.Marshal(map[string]interface{}{
		"name":      "ping",
		"arguments": map[string]interface{}{},
	})
	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "call_tool",
		Params:  params,
	}
	resp := srv.handleRequest(req)
	if resp.Error != nil {
		t.Fatalf("ping error: %+v", resp.Error)
	}
	result, ok := resp.Result.(map[string]string)
	if !ok || result["status"] != "ok" {
		t.Fatalf("result = %#v", resp.Result)
	}
	if ReadOrchestratorHeartbeat(town) == nil {
		t.Fatal("ping should touch orchestrator heartbeat")
	}
}

func TestServer_handleRequest_touchesHeartbeatOnInitialize(t *testing.T) {
	town := t.TempDir()
	mgr := NewManager(town)
	srv := NewServer(mgr)

	_ = srv.handleRequest(MCPRequest{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	hb := ReadOrchestratorHeartbeat(town)
	if hb == nil {
		t.Fatal("initialize should touch heartbeat")
	}
	if time.Since(hb.Timestamp) > time.Minute {
		t.Fatalf("heartbeat timestamp too old: %v", hb.Timestamp)
	}
}

func TestServer_listTools_includesPing(t *testing.T) {
	srv := NewServer(NewManager(t.TempDir()))
	resp := srv.handleRequest(MCPRequest{JSONRPC: "2.0", ID: 1, Method: "list_tools"})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result type %T", resp.Result)
	}
	found := false
	switch tools := result["tools"].(type) {
	case []map[string]interface{}:
		for _, tool := range tools {
			if tool["name"] == "ping" {
				found = true
				break
			}
		}
	case []interface{}:
		for _, raw := range tools {
			tool, ok := raw.(map[string]interface{})
			if ok && tool["name"] == "ping" {
				found = true
				break
			}
		}
	default:
		t.Fatalf("tools type %T", result["tools"])
	}
	if !found {
		t.Fatal("list_tools missing ping")
	}
}
