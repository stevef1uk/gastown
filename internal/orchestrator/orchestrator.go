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
	"github.com/steveyegge/gastown/internal/natsutil"
	"github.com/steveyegge/gastown/internal/util"
)

const (
	// PidFile is the path to the orchestrator PID file relative to the town root.
	PidFile = "daemon/orchestrator.pid"

	// OrchestratorCallTimeout is the NATS round-trip budget for lightweight MCP calls.
	OrchestratorCallTimeout = 2 * time.Second
	// OrchestratorWorkflowCallTimeout covers fetch_task / complete_task paths that run
	// bd subprocesses, planning sync, and phase transitions (often >2s).
	OrchestratorWorkflowCallTimeout = 60 * time.Second

	// OrchestratorNATSTimeoutEnv overrides OrchestratorWorkflowCallTimeout (e.g. "90s").
	OrchestratorNATSTimeoutEnv = "GT_ORCHESTRATOR_NATS_TIMEOUT"

	// maxCallRetries is the number of retries for transient NATS errors (timeout, no responders).
	maxCallRetries = 3
)

// orchestratorNATSURL returns the NATS URL for MCP calls (GT_ORCHESTRATOR_NATS_URL overrides in tests).
func orchestratorNATSURL() string {
	if u := os.Getenv("GT_ORCHESTRATOR_NATS_URL"); u != "" {
		return u
	}
	return nats.DefaultURL
}

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
	_ = EnsureInstancesDir(townRoot)
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
	TouchOrchestratorHeartbeat(townRoot)

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

// orchestratorWorkflowCallTimeout returns the NATS request budget for workflow MCP tools.
func orchestratorWorkflowCallTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv(OrchestratorNATSTimeoutEnv)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return OrchestratorWorkflowCallTimeout
}

// Call calls a tool on the orchestrator via NATS.
func Call(townRoot string, method string, params any) (json.RawMessage, error) {
	return CallWithTimeout(townRoot, method, params, OrchestratorCallTimeout)
}

// CallWithTimeout calls the orchestrator MCP service with a custom NATS request timeout.
// Retries on transient NATS errors (timeout, no responders) up to maxCallRetries times
// with exponential backoff to handle proxy restarts and temporary network issues.
func CallWithTimeout(townRoot string, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	if timeout <= 0 {
		timeout = OrchestratorCallTimeout
	}

	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      time.Now().UnixNano(),
		"method":  method,
		"params":  params,
	}
	data, _ := json.Marshal(req)

	var lastErr error
	for attempt := 0; attempt <= maxCallRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 2 * time.Second
			time.Sleep(backoff)
		}

		nc, err := natsutil.ConnectRobust(orchestratorNATSURL(), "gt-orchestrator-client")
		if err != nil {
			lastErr = fmt.Errorf("connecting to NATS: %w", err)
			continue
		}

		msg, err := nc.Request("gt.orchestrator.mcp", data, timeout)
		nc.Close()

		if err != nil {
			lastErr = fmt.Errorf("request to orchestrator failed: %w", err)
			if isRetriableNATSError(err) {
				continue
			}
			return nil, lastErr
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
	return nil, lastErr
}

func isRetriableNATSError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "nats: timeout") ||
		strings.Contains(msg, "nats: no responders") ||
		strings.Contains(msg, "connection refused")
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
	Hooks           StateHooks         `json:"hooks"`
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

	result, err := CallWithTimeout(townRoot, "call_tool", params, orchestratorWorkflowCallTimeout())
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
	result, err := CallWithTimeout(townRoot, "call_tool", params, orchestratorWorkflowCallTimeout())
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

	result, err := CallWithTimeout(townRoot, "call_tool", params, orchestratorWorkflowCallTimeout())
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

// DeleteWorkflow removes a workflow instance entirely.
func DeleteWorkflow(townRoot, workflowID string) error {
	// Check if orchestrator process seems alive (PID file + process exists).
	// Even if the PID is a zombie, we try MCP first and fall back to offline
	// when the NATS request fails (no responder).
	if running, _, _ := IsRunning(townRoot); running {
		if err := deleteWorkflowViaMCP(townRoot, workflowID); err != nil {
			// If NATS has no responder (orchestrator not listening), use offline path.
			if strings.Contains(err.Error(), "nats: no responders") ||
				strings.Contains(err.Error(), "connection refused") {
				return deleteWorkflowOffline(townRoot, workflowID)
			}
			return err
		}
		return nil
	}
	return deleteWorkflowOffline(townRoot, workflowID)
}

func deleteWorkflowOffline(townRoot, workflowID string) error {
	mgr := NewManager(townRoot)
	if err := mgr.LoadTownTemplates(); err != nil {
		return err
	}
	return mgr.DeleteWorkflow(workflowID)
}

// PauseWorkflow pauses an instance via the running orchestrator (or offline manager).
func PauseWorkflow(townRoot, workflowID string) (rig string, err error) {
	if running, _, _ := IsRunning(townRoot); running {
		return pauseWorkflowViaMCP(townRoot, workflowID)
	}
	mgr := NewManager(townRoot)
	if err := mgr.LoadTownTemplates(); err != nil {
		return "", err
	}
	return mgr.PauseWorkflow(workflowID)
}

// ResumeWorkflow resumes a paused instance.
func ResumeWorkflow(townRoot, workflowID string) error {
	if running, _, _ := IsRunning(townRoot); running {
		return resumeWorkflowViaMCP(townRoot, workflowID)
	}
	mgr := NewManager(townRoot)
	if err := mgr.LoadTownTemplates(); err != nil {
		return err
	}
	return mgr.ResumeWorkflow(workflowID)
}

// PauseWorkflowsForRig pauses all running workflows bound to rig.
func PauseWorkflowsForRig(townRoot, rig string) ([]string, error) {
	if running, _, _ := IsRunning(townRoot); running {
		return pauseRigWorkflowsViaMCP(townRoot, rig)
	}
	mgr := NewManager(townRoot)
	if err := mgr.LoadTownTemplates(); err != nil {
		return nil, err
	}
	return mgr.PauseRunningWorkflowsForRig(rig)
}

func pauseWorkflowViaMCP(townRoot, workflowID string) (string, error) {
	result, err := Call(townRoot, "call_tool", map[string]interface{}{
		"name": "pause_workflow",
		"arguments": map[string]interface{}{
			"workflow_id": workflowID,
		},
	})
	if err != nil {
		return "", err
	}
	var data struct {
		Rig string `json:"rig"`
	}
	if err := json.Unmarshal(result, &data); err != nil {
		return "", err
	}
	return data.Rig, nil
}

func resumeWorkflowViaMCP(townRoot, workflowID string) error {
	_, err := Call(townRoot, "call_tool", map[string]interface{}{
		"name": "resume_workflow",
		"arguments": map[string]interface{}{
			"workflow_id": workflowID,
		},
	})
	return err
}

func deleteWorkflowViaMCP(townRoot, workflowID string) error {
	_, err := Call(townRoot, "call_tool", map[string]interface{}{
		"name": "delete_workflow",
		"arguments": map[string]interface{}{
			"workflow_id": workflowID,
		},
	})
	return err
}

func pauseRigWorkflowsViaMCP(townRoot, rig string) ([]string, error) {
	result, err := Call(townRoot, "call_tool", map[string]interface{}{
		"name": "pause_workflow",
		"arguments": map[string]interface{}{
			"rig": rig,
		},
	})
	if err != nil {
		return nil, err
	}
	var data struct {
		WorkflowIDs []string `json:"workflow_ids"`
	}
	if err := json.Unmarshal(result, &data); err != nil {
		return nil, err
	}
	return data.WorkflowIDs, nil
}
