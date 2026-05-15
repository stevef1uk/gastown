package orchestrator

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/nats-io/nats.go"
)

// MCPRequest represents a JSON-RPC request from an MCP client.
type MCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// MCPResponse represents a JSON-RPC response to an MCP client.
type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

// MCPError represents a JSON-RPC error.
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Server implements a minimal MCP server over stdio.
type Server struct {
	orchestrator *Manager
}

// NewServer creates a new MCP server.
func NewServer(mgr *Manager) *Server {
	return &Server{orchestrator: mgr}
}

// Serve starts the MCP server loop reading from stdin and writing to stdout.
func (s *Server) Serve() error {
	dec := json.NewDecoder(os.Stdin)
	for {
		var req MCPRequest
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		resp := s.handleRequest(req)
		json.NewEncoder(os.Stdout).Encode(resp)
	}
}

// ListenNATS starts a NATS listener for MCP requests.
func (s *Server) ListenNATS(url string) error {
	nc, err := nats.Connect(url)
	if err != nil {
		return err
	}

	_, err = nc.Subscribe("gt.orchestrator.mcp", func(msg *nats.Msg) {
		var req MCPRequest
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			return
		}

		resp := s.handleRequest(req)
		respData, _ := json.Marshal(resp)
		msg.Respond(respData)
	})

	return err
}

func (s *Server) handleRequest(req MCPRequest) MCPResponse {
	switch req.Method {
	case "initialize":
		return MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"serverInfo": map[string]string{
					"name":    "gt-orchestrator",
					"version": "0.1.0",
				},
			},
		}
	case "list_tools":
		return MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"tools": []map[string]interface{}{
					{
						"name":        "fetch_task",
						"description": "Fetch the next task for an agent",
						"inputSchema": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"agent_id": map[string]string{"type": "string"},
							},
							"required": []string{"agent_id"},
						},
					},
					{
						"name":        "complete_task",
						"description": "Report task completion and trigger transition",
						"inputSchema": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"workflow_id": map[string]string{"type": "string"},
								"outcome":     map[string]string{"type": "string"},
							},
							"required": []string{"workflow_id", "outcome"},
						},
					},
					{
						"name":        "start_workflow",
						"description": "Start a new workflow instance from a template",
						"inputSchema": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"template_id": map[string]string{"type": "string"},
								"variables": map[string]interface{}{
									"type":                 "object",
									"additionalProperties": map[string]string{"type": "string"},
								},
							},
							"required": []string{"template_id"},
						},
					},
					{
						"name":        "get_workflow_status",
						"description": "Get workflow instance status (all instances if workflow_id omitted)",
						"inputSchema": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"workflow_id": map[string]string{"type": "string"},
							},
						},
					},
				},
			},
		}
	case "call_tool":
		return s.handleCallTool(req)
	default:
		return MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &MCPError{Code: -32601, Message: fmt.Sprintf("Method %s not found", req.Method)},
		}
	}
}

func (s *Server) handleCallTool(req MCPRequest) MCPResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return MCPResponse{JSONRPC: "2.0", ID: req.ID, Error: &MCPError{Code: -32602, Message: err.Error()}}
	}
	fmt.Printf("[MCP] Call Tool: %s\n", params.Name)

	switch params.Name {
	case "fetch_task":
		var args struct {
			AgentID string `json:"agent_id"`
		}
		json.Unmarshal(params.Arguments, &args)
		fmt.Printf("[MCP] fetch_task for agent: %s\n", args.AgentID)
		task, err := s.orchestrator.FetchTask(args.AgentID)
		if err != nil {
			return MCPResponse{JSONRPC: "2.0", ID: req.ID, Error: &MCPError{Code: -32000, Message: err.Error()}}
		}
		return MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: task}
	case "complete_task":
		var args struct {
			WorkflowID string `json:"workflow_id"`
			Outcome    string `json:"outcome"`
		}
		json.Unmarshal(params.Arguments, &args)
		nextState, err := s.orchestrator.CompleteTask(args.WorkflowID, args.Outcome)
		if err != nil {
			return MCPResponse{JSONRPC: "2.0", ID: req.ID, Error: &MCPError{Code: -32000, Message: err.Error()}}
		}
		fmt.Printf("[MCP] complete_task %s -> next state: %s\n", args.WorkflowID, nextState)
		return MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]string{"next_state": nextState}}
	case "start_workflow":
		var args struct {
			TemplateID string            `json:"template_id"`
			Variables  map[string]string `json:"variables"`
		}
		json.Unmarshal(params.Arguments, &args)
		rig := ""
		if args.Variables != nil {
			rig = args.Variables["rig"]
		}
		if s.orchestrator.HasActiveWorkflow(args.TemplateID, rig) {
			return MCPResponse{JSONRPC: "2.0", ID: req.ID, Error: &MCPError{
				Code:    -32000,
				Message: fmt.Sprintf("workflow %q already active for rig %q", args.TemplateID, rig),
			}}
		}
		workflowID, err := s.orchestrator.StartWorkflow(args.TemplateID, args.Variables)
		if err != nil {
			return MCPResponse{JSONRPC: "2.0", ID: req.ID, Error: &MCPError{Code: -32000, Message: err.Error()}}
		}
		return MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]string{"workflow_id": workflowID}}
	case "get_workflow_status":
		var args struct {
			WorkflowID string `json:"workflow_id"`
		}
		json.Unmarshal(params.Arguments, &args)
		statuses, err := s.orchestrator.GetWorkflowStatus(args.WorkflowID)
		if err != nil {
			return MCPResponse{JSONRPC: "2.0", ID: req.ID, Error: &MCPError{Code: -32000, Message: err.Error()}}
		}
		return MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{"workflows": statuses}}
	default:
		return MCPResponse{JSONRPC: "2.0", ID: req.ID, Error: &MCPError{Code: -32601, Message: "Tool not found"}}
	}
}
