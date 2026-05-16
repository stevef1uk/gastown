package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/steveyegge/gastown/internal/util"
)

const (
	// PidFile is the path to the orchestrator PID file relative to the town root.
	PidFile = "daemon/orchestrator.pid"
)

// IsRunning checks if the orchestrator is running by checking its PID file.
func IsRunning(townRoot string) (bool, int, error) {
	pidPath := filepath.Join(townRoot, PidFile)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, nil
		}
		return false, 0, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false, 0, nil // Invalid PID file
	}

	// Check if process exists
	process, err := os.FindProcess(pid)
	if err != nil {
		return false, 0, nil
	}

	// On Unix, FindProcess always succeeds, so we need to send signal 0
	err = process.Signal(syscall.Signal(0))
	if err != nil {
		return false, 0, nil
	}

	return true, pid, nil
}

// Start starts the orchestrator in the background.
func Start(townRoot string) error {
	running, _, _ := IsRunning(townRoot)
	if running {
		return nil
	}

	gtPath, err := os.Executable()
	if err != nil {
		return err
	}

	// Start gt-orchestrator. Since we don't have a separate binary yet,
	// we'll assume 'gt orchestrator' subcommand will be added,
	// or we just run the gt-orchestrator binary if it exists.
	// For now, let's assume there's a 'gt-orchestrator' binary in the same dir as 'gt'.
	orchPath := filepath.Join(filepath.Dir(gtPath), "gt-orchestrator")
	if _, err := os.Stat(orchPath); os.IsNotExist(err) {
		// Fallback to 'gt orchestrator' if we add it to the main binary later
		orchPath = gtPath
	}

	cmd := exec.Command(orchPath)
	if orchPath == gtPath {
		cmd.Args = []string{gtPath, "orchestrator", "run"}
	}
	cmd.Dir = townRoot

	// Create a log file for the orchestrator
	logDir := filepath.Join(townRoot, "logs")
	_ = os.MkdirAll(logDir, 0755)
	logFile, err := os.Create(filepath.Join(logDir, "orchestrator.log"))
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	util.SetDetachedProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return err
	}

	// Write PID file
	pidPath := filepath.Join(townRoot, PidFile)
	if err := os.MkdirAll(filepath.Dir(pidPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0644); err != nil {
		return err
	}

	return nil
}

// Stop stops the orchestrator process.
func Stop(townRoot string) error {
	running, pid, err := IsRunning(townRoot)
	if err != nil {
		return err
	}
	if !running {
		return nil
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}

	// Try graceful SIGTERM
	process.Signal(syscall.SIGTERM)

	// Wait a bit
	for i := 0; i < 10; i++ {
		time.Sleep(100 * time.Millisecond)
		if running, _, _ := IsRunning(townRoot); !running {
			os.Remove(filepath.Join(townRoot, PidFile))
			return nil
		}
	}

	// Force kill if still running
	os.Remove(filepath.Join(townRoot, PidFile))
	return nil
}

// Call calls a tool on the orchestrator via NATS.
func Call(townRoot string, method string, params any) (json.RawMessage, error) {
	// [TODO] Get NATS URL from town settings
	natsURL := nats.DefaultURL

	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("connecting to NATS: %w", err)
	}
	defer nc.Close()

	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      time.Now().UnixNano(),
		"method":  method,
		"params":  params,
	}
	data, _ := json.Marshal(req)

	msg, err := nc.Request("gt.orchestrator.mcp", data, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("request to orchestrator failed: %w", err)
	}

	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("orchestrator error: %s", resp.Error.Message)
	}

	return resp.Result, nil
}

// StartWorkflow initiates a workflow via the orchestrator.
func StartWorkflow(townRoot string, templateID string, vars map[string]string) (string, error) {
	params := map[string]interface{}{
		"name": "start_workflow",
		"arguments": map[string]interface{}{
			"template_id": templateID,
			"variables":   vars,
		},
	}

	result, err := Call(townRoot, "call_tool", params)
	if err != nil {
		return "", err
	}

	var data struct {
		WorkflowID string `json:"workflow_id"`
	}
	if err := json.Unmarshal(result, &data); err != nil {
		return "", err
	}

	return data.WorkflowID, nil
}

// Task represents a task assigned by the orchestrator.
type Task struct {
	WorkflowID      string             `json:"workflow_id"`
	TemplateID      string             `json:"template_id"`
	State           string             `json:"state"`
	Role            string             `json:"role"`
	Rig             string             `json:"rig"`
	SystemPrompt    string             `json:"system_prompt"`
	TaskPrompt      string             `json:"task_prompt"`
	Instructions    string             `json:"instructions"`
	AllowedOutcomes []string           `json:"allowed_outcomes"`
	Validation      WorkflowValidation `json:"validation"`
	PendingRework   *WorkflowRework    `json:"pending_rework,omitempty"`
}

// FetchTask fetches the next task for an agent.
func FetchTask(townRoot string, agentID string) (*Task, error) {
	params := map[string]interface{}{
		"name": "fetch_task",
		"arguments": map[string]interface{}{
			"agent_id": agentID,
		},
	}

	result, err := Call(townRoot, "call_tool", params)
	if err != nil {
		if strings.Contains(err.Error(), "no task available") {
			return nil, nil
		}
		return nil, err
	}

	var task Task
	if err := json.Unmarshal(result, &task); err != nil {
		return nil, err
	}

	if task.WorkflowID == "" || (task.SystemPrompt == "" && task.Instructions == "" && task.TaskPrompt == "") {
		return nil, nil
	}
	if task.TaskPrompt != "" && task.Instructions == "" {
		task.Instructions = task.TaskPrompt
	}

	return &task, nil
}

// OrchestratorAgentID builds the agent_id passed to fetch_task (rig-scoped when GT_RIG is set).
func OrchestratorAgentID(role, rig string) string {
	if rig != "" {
		return rig + "/" + role
	}
	return role
}

// GetWorkflowStatuses returns workflow snapshots via the running orchestrator.
func GetWorkflowStatuses(townRoot, workflowID string) ([]WorkflowStatus, error) {
	params := map[string]interface{}{
		"name": "get_workflow_status",
		"arguments": map[string]interface{}{
			"workflow_id": workflowID,
		},
	}
	result, err := Call(townRoot, "call_tool", params)
	if err != nil {
		return nil, err
	}
	var data struct {
		Workflows []WorkflowStatus `json:"workflows"`
	}
	if err := json.Unmarshal(result, &data); err != nil {
		return nil, err
	}
	return data.Workflows, nil
}

// ResetWorkflow rewinds a workflow instance via the running orchestrator.
func ResetWorkflow(townRoot, workflowID, toState string) (string, error) {
	params := map[string]interface{}{
		"name": "reset_workflow",
		"arguments": map[string]interface{}{
			"workflow_id": workflowID,
			"to_state":    toState,
		},
	}
	result, err := Call(townRoot, "call_tool", params)
	if err != nil {
		return "", err
	}
	var data struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(result, &data); err != nil {
		return "", err
	}
	return data.State, nil
}

// CompleteTask reports task completion to the orchestrator.
func CompleteTask(townRoot string, workflowID string, outcome string, agentID, summary, feedback string) (string, error) {
	args := map[string]interface{}{
		"workflow_id": workflowID,
		"outcome":     outcome,
	}
	if agentID != "" {
		args["agent_id"] = agentID
	}
	if summary != "" {
		args["summary"] = summary
	}
	if feedback != "" {
		args["feedback"] = feedback
	}
	params := map[string]interface{}{
		"name":      "complete_task",
		"arguments": args,
	}

	result, err := Call(townRoot, "call_tool", params)
	if err != nil {
		return "", err
	}

	var data struct {
		NextState string `json:"next_state"`
	}
	if err := json.Unmarshal(result, &data); err != nil {
		return "", err
	}

	return data.NextState, nil
}
