package orchestrator

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/nats-io/nats.go"
	"github.com/steveyegge/gastown/internal/natsutil"
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
	nc           *nats.Conn
}

// NewServer creates a new MCP server.
func NewServer(mgr *Manager) *Server {
	return &Server{orchestrator: mgr}
}

// IsConnected reports whether the NATS connection is alive.
func (s *Server) IsConnected() bool {
	return s.nc != nil && s.nc.IsConnected()
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
	if s.nc != nil {
		s.nc.Close()
		s.nc = nil
	}
	nc, err := natsutil.ConnectRobustService(url, "gt-orchestrator")
	if err != nil {
		return err
	}
	s.nc = nc

	sub, err := nc.Subscribe("gt.orchestrator.mcp", func(msg *nats.Msg) {
		var req MCPRequest
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			log.Printf("[orchestrator] bad MCP request: %v", err)
			return
		}

		resp := s.handleRequest(req)
		respData, _ := json.Marshal(resp)
		if err := msg.Respond(respData); err != nil {
			log.Printf("[orchestrator] failed to respond to MCP request: %v", err)
		}
	})
	if err != nil {
		nc.Close()
		s.nc = nil
		return err
	}
	if err := nc.Flush(); err != nil {
		sub.Unsubscribe()
		nc.Close()
		s.nc = nil
		return fmt.Errorf("flushing NATS subscription: %w", err)
	}

	log.Printf("[orchestrator] subscribed to gt.orchestrator.mcp on %s", url)
	return nil
}

func (s *Server) handleRequest(req MCPRequest) MCPResponse {
	if s.orchestrator != nil && s.orchestrator.townRoot != "" {
		TouchOrchestratorHeartbeat(s.orchestrator.townRoot)
	}
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
						"name":        "ping",
						"description": "Liveness probe (no-op; updates orchestrator heartbeat)",
						"inputSchema": map[string]interface{}{
							"type":       "object",
							"properties": map[string]interface{}{},
						},
					},
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
					{
						"name":        "reset_workflow",
						"description": "Rewind a workflow to an earlier state (e.g. design after deleting architecture.md)",
						"inputSchema": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"workflow_id": map[string]string{"type": "string"},
								"to_state":    map[string]string{"type": "string"},
							},
							"required": []string{"workflow_id"},
						},
					},
					{
						"name":        "pause_workflow",
						"description": "Pause workflow(s); pass workflow_id or rig (pauses all running on that rig)",
						"inputSchema": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"workflow_id": map[string]string{"type": "string"},
								"rig":         map[string]string{"type": "string"},
							},
						},
					},
				{
					"name":        "delete_workflow",
					"description": "Permanently delete a workflow instance from the registry",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"workflow_id": map[string]string{"type": "string"},
						},
						"required": []string{"workflow_id"},
					},
				},
				{
					"name":        "resume_workflow",
					"description": "Resume a paused workflow instance",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"workflow_id": map[string]string{"type": "string"},
						},
						"required": []string{"workflow_id"},
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
	case "ping":
		return MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]string{"status": "ok"}}
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
		WorkflowID string            `json:"workflow_id"`
		Outcome    string            `json:"outcome"`
		AgentID    string            `json:"agent_id"`
		Summary    string            `json:"summary"`
		Feedback   string            `json:"feedback"`
		Variables  map[string]string `json:"variables"`
	}
	json.Unmarshal(params.Arguments, &args)
	if args.AgentID != "" {
		fmt.Printf("[MCP] complete_task for agent: %s\n", args.AgentID)
	}
	nextState, err := s.orchestrator.CompleteTask(args.WorkflowID, args.Outcome, args.AgentID, args.Summary, args.Feedback, args.Variables)
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
	case "reset_workflow":
		var args struct {
			WorkflowID string `json:"workflow_id"`
			ToState    string `json:"to_state"`
		}
		json.Unmarshal(params.Arguments, &args)
		next, err := s.orchestrator.ResetWorkflow(args.WorkflowID, args.ToState)
		if err != nil {
			return MCPResponse{JSONRPC: "2.0", ID: req.ID, Error: &MCPError{Code: -32000, Message: err.Error()}}
		}
		return MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]string{"state": next}}
	case "pause_workflow":
		var args struct {
			WorkflowID string `json:"workflow_id"`
			Rig        string `json:"rig"`
		}
		json.Unmarshal(params.Arguments, &args)
		if args.Rig != "" {
			ids, err := s.orchestrator.PauseRunningWorkflowsForRig(args.Rig)
			if err != nil {
				return MCPResponse{JSONRPC: "2.0", ID: req.ID, Error: &MCPError{Code: -32000, Message: err.Error()}}
			}
			return MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{
				"workflow_ids": ids,
				"rig":          args.Rig,
			}}
		}
		if args.WorkflowID == "" {
			return MCPResponse{JSONRPC: "2.0", ID: req.ID, Error: &MCPError{Code: -32602, Message: "workflow_id or rig required"}}
		}
		rig, err := s.orchestrator.PauseWorkflow(args.WorkflowID)
		if err != nil {
			return MCPResponse{JSONRPC: "2.0", ID: req.ID, Error: &MCPError{Code: -32000, Message: err.Error()}}
		}
		return MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{
			"workflow_id": args.WorkflowID,
			"rig":         rig,
		}}
	case "delete_workflow":
		var args struct {
			WorkflowID string `json:"workflow_id"`
		}
		json.Unmarshal(params.Arguments, &args)
		if err := s.orchestrator.DeleteWorkflow(args.WorkflowID); err != nil {
			return MCPResponse{JSONRPC: "2.0", ID: req.ID, Error: &MCPError{Code: -32000, Message: err.Error()}}
		}
		return MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]string{"workflow_id": args.WorkflowID, "status": "deleted"}}
	case "resume_workflow":
		var args struct {
			WorkflowID string `json:"workflow_id"`
		}
		json.Unmarshal(params.Arguments, &args)
		if err := s.orchestrator.ResumeWorkflow(args.WorkflowID); err != nil {
			return MCPResponse{JSONRPC: "2.0", ID: req.ID, Error: &MCPError{Code: -32000, Message: err.Error()}}
		}
		return MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]string{"workflow_id": args.WorkflowID, "status": "running"}}
	default:
		return MCPResponse{JSONRPC: "2.0", ID: req.ID, Error: &MCPError{Code: -32601, Message: "Tool not found"}}
	}
}
