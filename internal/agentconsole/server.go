package agentconsole

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sync"

	"github.com/nats-io/nats.go"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/events"
	"github.com/steveyegge/gastown/internal/orchestrator"
	"github.com/steveyegge/gastown/internal/session"
)

// Agent represents a Gas Town agent (deacon, mayor, witness, refinery, crew, polecat).
type Agent struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	Rig       string    `json:"rig,omitempty"`
	AgentType string    `json:"agent_type"`
	PID       int       `json:"pid,omitempty"`
	Since     time.Time `json:"since,omitempty"`
	Activity       string `json:"activity,omitempty"`
	WorkflowID     string `json:"workflow_id,omitempty"`
	WorkflowState  string `json:"workflow_state,omitempty"`
	WorkflowActive bool   `json:"workflow_active,omitempty"`
}

// Server is the agent console HTTP server.
type Server struct {
	townRoot   string
	natsURL    string
	nc         *nats.Conn
	natsSubs   map[string]*nats.Subscription
	natsMu     sync.Mutex
}

// NewServer creates a new agent console server.
func NewServer(townRoot string) (*Server, error) {
	s := &Server{
		townRoot: townRoot,
		natsSubs: make(map[string]*nats.Subscription),
	}

	// Initialize session registry so rigs and prefixes are resolved correctly (gt-z9xk)
	if err := session.InitRegistry(townRoot); err != nil {
		fmt.Fprintf(os.Stderr, "[agent-console] Warning: failed to initialize town registry: %v\n", err)
	}

	// Try to connect to NATS
	natsURL := os.Getenv("GT_NATS_URL")
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}
	nc, err := nats.Connect(natsURL)
	if err == nil {
		s.nc = nc
		s.natsURL = natsURL
	} else {
		fmt.Fprintf(os.Stderr, "[agent-console] NATS not available: %v (messaging disabled)\n", err)
	}

	return s, nil
}

// Close cleans up NATS subscriptions.
func (s *Server) Close() {
	if s.nc != nil {
		s.nc.Close()
	}
}

// RegisterRoutes registers HTTP handlers.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/agents", s.handleAgents)
	mux.HandleFunc("/api/agents/", s.handleAgentDetail)
	mux.HandleFunc("/api/agents/{id}/message", s.handleSendMessage)
	mux.HandleFunc("/api/agents/{id}/logs", s.handleAgentLogs)
	mux.HandleFunc("/api/agents/{id}/stream", s.handleAgentStream)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/workflows", s.handleWorkflows)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles))))
}

// handleAgents returns a JSON list of all agents.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	agents := s.discoverAgents()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agents)
}

func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.loadWorkflows())
}

// writeJSONError sends a JSON error response.
func (s *Server) writeJSONError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// handleAgentDetail returns details for a specific agent.
func (s *Server) handleAgentDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		// Fallback for Go < 1.22
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) >= 4 {
			id = parts[3]
		}
	}

	agents := s.discoverAgents()
	for _, a := range agents {
		if a.ID == id {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(a)
			return
		}
	}
	s.writeJSONError(w, "agent not found", http.StatusNotFound)
}

// handleSendMessage sends a nudge to an agent via NATS.
func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) >= 4 {
			id = parts[3]
		}
	}

	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSONError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Use the transport-aware session provider to deliver the nudge.
	// This ensures it works for both Tmux and NATS providers, and
	// correctly logs to the activity feed to wake up idle agents.
	sp := session.GetDefaultProvider(s.townRoot)
	sessionName := s.agentIDToSessionName(id)
	sender := "overseer (web console)"
	err := sp.NudgeSession(r.Context(), sessionName, req.Message, sender)
	if err != nil {
		s.writeJSONError(w, fmt.Sprintf("failed to deliver nudge: %v", err), http.StatusInternalServerError)
		return
	}

	// Log to activity feed so idle agents wake up immediately
	_ = events.LogFeed(events.TypeNudge, sender, events.NudgePayload("", sessionName, req.Message))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "queued", "via": "nudge", "session": sessionName})
}

// handleAgentLogs returns recent log lines for an agent.
func (s *Server) handleAgentLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) >= 4 {
			id = parts[3]
		}
	}

	agent, ok := s.lookupAgent(id)
	sessionName := s.agentIDToSessionName(id)
	rig, role, workerName := "", "", ""
	if ok {
		rig, role = agent.Rig, agent.Role
		if agent.Rig != "" && (agent.Role == "crew" || agent.Role == "polecat") && agent.Name != sessionName && agent.Name != friendlyRigAgentName(agent.Rig, agent.Role, sessionName) {
			workerName = agent.Name
		}
	}
	logs := s.readAgentLogs(sessionName, rig, role, workerName, 50)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"agent":   id,
		"session": sessionName,
		"logs":    logs,
	})
}

// handleAgentStream streams agent activity via SSE.
func (s *Server) handleAgentStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) >= 4 {
			id = parts[3]
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeJSONError(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Subscribe to NATS nudges for this agent
	var sub *nats.Subscription
	msgChan := make(chan *nats.Msg, 10)
	if s.nc != nil {
		subj := fmt.Sprintf("gastown.nudge.%s", id)
		sub, _ = s.nc.ChanSubscribe(subj, msgChan)
		defer sub.Unsubscribe()
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	agent, _ := s.lookupAgent(id)
	lastLogCount := 0
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-msgChan:
			fmt.Fprintf(w, "event: nudge\ndata: %s\n\n", msg.Data)
			flusher.Flush()
		case <-ticker.C:
			// Check for new logs
			workerName := ""
			sn := s.agentIDToSessionName(id)
			if agent.Rig != "" && (agent.Role == "crew" || agent.Role == "polecat") && agent.Name != sn && agent.Name != friendlyRigAgentName(agent.Rig, agent.Role, sn) {
				workerName = agent.Name
			}
			logs := s.readAgentLogs(sn, agent.Rig, agent.Role, workerName, 100)
			if len(logs) != lastLogCount {
				newLogs := logs
				if len(logs) > lastLogCount && lastLogCount > 0 {
					newLogs = logs[lastLogCount:]
				}
				data, _ := json.Marshal(newLogs)
				fmt.Fprintf(w, "event: logs\ndata: %s\n\n", data)
				flusher.Flush()
				lastLogCount = len(logs)
			}
		}
	}
}

// handleStatus returns overall system status.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	agents := s.discoverAgents()
	running := 0
	for _, a := range agents {
		if a.Status == "running" {
			running++
		}
	}

	orchRunning, orchPID, _ := orchestrator.IsRunning(s.townRoot)
	status := map[string]interface{}{
		"agents_total":          len(agents),
		"agents_running":        running,
		"nats_connected":        s.nc != nil && s.nc.IsConnected(),
		"town_root":             s.townRoot,
		"orchestrator_running":  orchRunning,
		"orchestrator_pid":      orchPID,
		"workflows":             s.loadWorkflows(),
		"timestamp":             time.Now().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleIndex serves the main HTML page.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

// discoverAgents finds all agents in the workspace.
func (s *Server) discoverAgents() []Agent {
	// Re-initialize registry to pick up new rigs (gt-z9xk)
	_ = session.InitRegistry(s.townRoot)

	var agents []Agent

	workflows := s.loadWorkflows()

	// Orchestrator service (gt orchestrator run)
	agents = append(agents, s.inspectOrchestrator(workflows))

	// 1. Explicitly add town-level infrastructure agents.
	// These are singletons that should always be visible, even when stopped.
	infrastructureTownAgents := []string{
		constants.RoleMayor,
		constants.RoleDeacon,
		constants.RolePlanner,
		constants.RoleSetup,
		constants.RoleMechanic,
	}
	added := make(map[string]bool)
	for _, role := range infrastructureTownAgents {
		sessionName := "hq-" + role
		agents = append(agents, s.inspectAgent(sessionName, "", role))
		added[sessionName] = true
	}

	// 2. Dynamically discover other town-level agents from PID files (e.g. dogs).
	// These use the 'hq-' prefix and live at the town root.
	pidDir := filepath.Join(s.townRoot, ".gt-nats-pids")
	if entries, err := os.ReadDir(pidDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasPrefix(name, "hq-") || added[name] {
				continue
			}
			// Match town-level pattern: hq-<role>
			// We'll treat anything hq-* as a town agent if not already added.
			parts := strings.Split(name, "-")
			role := parts[len(parts)-1]
			agents = append(agents, s.inspectAgent(name, "", role))
			added[name] = true
		}
	}

	// Use the session package's registry to find all rigs (handles rigs.json location fallback)
	registry := session.DefaultRegistry()
	for rigName := range registry.AllRigs() {
		// Check rig-specific agents (Witness, Refinery, Architect, QA)
		prefix := registry.PrefixForRig(rigName)
		
		// Witness
		witnessID := session.WitnessSessionName(prefix, rigName)
		agents = append(agents, s.inspectAgent(witnessID, rigName, "witness"))
		
		// Refinery
		refineryID := session.RefinerySessionName(prefix, rigName)
		agents = append(agents, s.inspectAgent(refineryID, rigName, "refinery"))
		
		// Architect
		architectID := session.ArchitectSessionName(prefix, rigName)
		agents = append(agents, s.inspectAgent(architectID, rigName, "architect"))
		
		// QA
		qaID := session.QASessionName(prefix, rigName)
		agents = append(agents, s.inspectAgent(qaID, rigName, "qa"))

		// Pipeline polecat (orchestrated rig-flow)
		rigPolecatID := session.RigPolecatSessionName(prefix, rigName)
		agents = append(agents, s.inspectAgent(rigPolecatID, rigName, "polecat"))

		// Mechanic (Rig view of town-level mechanic)
		// The town-level hq-mechanic patrols logs for all rigs, so we show its
		// status in each rig group to meet user expectations.
		agents = append(agents, s.inspectAgent(session.MechanicSessionName(), rigName, constants.RoleMechanic))

		// Check crew
		crewDir := filepath.Join(s.townRoot, rigName, "crew")
		if entries, err := os.ReadDir(crewDir); err == nil {
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
					id := session.CrewSessionName(prefix, e.Name())
					a := s.inspectAgent(id, rigName, "crew")
					a.Name = e.Name() // Override name with worker name
					agents = append(agents, a)
				}
			}
		}

		// Check polecats
		polecatDir := filepath.Join(s.townRoot, rigName, "polecats")
		if entries, err := os.ReadDir(polecatDir); err == nil {
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
					id := session.PolecatSessionName(prefix, e.Name())
					a := s.inspectAgent(id, rigName, "polecat")
					a.Name = e.Name() // Override name with worker name
					agents = append(agents, a)
				}
			}
		}
	}

	enrichAgentsWithWorkflows(agents, workflows)
	return agents
}

func (s *Server) inspectOrchestrator(workflows []WorkflowInfo) Agent {
	a := Agent{
		ID:        "orchestrator",
		Name:      "Orchestrator",
		Role:      "orchestrator",
		AgentType: "gt-orchestrator",
	}
	running, pid, _ := orchestrator.IsRunning(s.townRoot)
	if running && pid > 0 {
		a.PID = pid
		a.Status = "running"
		if stat, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); err == nil {
			a.Since = stat.ModTime()
		}
	} else {
		a.Status = "stopped"
	}
	a.Activity = orchestratorActivity(workflows)
	logs := s.readAgentLogs("orchestrator", "", "orchestrator", "", 3)
	if len(logs) > 0 && a.Activity == "Idle" {
		a.Activity = logs[len(logs)-1]
	}
	return a
}

func (s *Server) lookupAgent(id string) (Agent, bool) {
	for _, a := range s.discoverAgents() {
		if a.ID == id {
			return a, true
		}
	}
	return Agent{}, false
}

// inspectAgent checks the status of a specific agent.
func (s *Server) inspectAgent(sessionName, rig, role string) Agent {
	a := Agent{
		ID:   sessionName,
		Role: role,
		Rig:  rig,
		Name: sessionName, // Default to session name, overridden for crew/polecat
	}

	pid := s.findAgentPID(sessionName, rig, role)
	if pid > 0 {
		a.PID = pid
		a.Status = "running"
		// Get process start time
		if stat, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); err == nil {
			a.Since = stat.ModTime()
		}
	} else {
		a.Status = "stopped"
	}

	// Set agent type (even if stopped, to show correct icon/label)
	a.AgentType = s.detectAgentType(sessionName)

	workerName := ""
	if rig != "" && (role == "crew" || role == "polecat") && a.Name != sessionName {
		workerName = a.Name
	}

	// Provide friendly names for infrastructure agents
	if strings.HasPrefix(sessionName, "hq-") {
		roleName := strings.TrimPrefix(sessionName, "hq-")
		a.Name = strings.Title(roleName)
	} else if rig != "" && a.Name == sessionName {
		a.Name = friendlyRigAgentName(rig, role, sessionName)
	}

	// Try to read activity/state
	logs := s.readAgentLogs(sessionName, rig, role, workerName, 5)
	if len(logs) > 0 {
		a.Activity = logs[len(logs)-1]
	}

	// Read state for patrol count etc.
	state := s.readAgentState(sessionName, rig, role, a.Name)
	if state.PatrolCount > 0 {
		a.Activity = fmt.Sprintf("Patrol #%d", state.PatrolCount)
	}

	return a
}

func friendlyRigAgentName(rig, role, sessionName string) string {
	switch role {
	case "architect":
		return "Architect"
	case "qa":
		return "QA"
	case "witness":
		return "Witness"
	case "refinery":
		return "Refinery"
	case "polecat":
		if strings.HasSuffix(sessionName, "-polecat") || strings.HasSuffix(sessionName, rig+"-polecat") {
			return "Polecat (pipeline)"
		}
		return "Polecat"
	default:
		return strings.Title(role)
	}
}

// findAgentPID finds the PID of an agent by its session name.
// NATS gt-agent processes pass GT_SESSION / GT_ROLE in the environment, not argv.
func (s *Server) findAgentPID(sessionName, rig, role string) int {
	// Check wrapper PID files. NATS provider writes to sessionID (no .pid
	// extension), but legacy code may use .pid — check both.
	for _, pidName := range []string{sessionName, sessionName + ".pid"} {
		pidFile := filepath.Join(s.townRoot, ".gt-nats-pids", pidName)
		if data, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				// Verify process exists
				if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); err == nil {
					return pid
				}
			}
		}
	}

	// Fallback: scan gt-agent PIDs in this town and match GT_SESSION or GT_ROLE.
	searchRole := role
	if searchRole == "" && strings.Contains(sessionName, "-") {
		parts := strings.Split(sessionName, "-")
		searchRole = parts[len(parts)-1]
	}
	cmd := exec.Command("pgrep", "-f", "gt-agent")
	out, _ := cmd.Output()
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(strings.Fields(line)[0])
		if err != nil {
			continue
		}
		if !s.pidInTown(pid) {
			continue
		}
		env := readProcEnviron(pid)
		if procEnvironMatches(env, "GT_SESSION", sessionName) {
			return pid
		}
		if rig != "" && searchRole != "" {
			qualified := rig + "/" + searchRole
			if procEnvironMatches(env, "GT_ROLE", qualified) || procEnvironMatches(env, "GT_ROLE", searchRole) {
				return pid
			}
		}
	}
	return 0
}

func (s *Server) pidInTown(pid int) bool {
	procCwd, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "cwd"))
	if err != nil {
		return false
	}
	return strings.HasPrefix(procCwd, s.townRoot)
}

func readProcEnviron(pid int) string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil {
		return ""
	}
	return string(data)
}

func procEnvironMatches(environ, key, value string) bool {
	if environ == "" || value == "" {
		return false
	}
	needle := key + "=" + value
	return strings.Contains(environ, needle+"\x00") || strings.HasSuffix(environ, needle)
}

// detectAgentType determines what agent binary is used.
func (s *Server) detectAgentType(sessionName string) string {
	// Check wrapper log for agent type
	logDir := filepath.Join(s.townRoot, "logs", "sessions")
	logPaths := []string{
		filepath.Join(logDir, sessionName+".log"),
		filepath.Join(logDir, sessionName+".wrapper.log"),
	}

	for _, path := range logPaths {
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			if strings.Contains(content, "gt-agent") {
				return "gt-agent"
			}
			if strings.Contains(content, "opencode") {
				return "opencode"
			}
			if strings.Contains(content, "claude") {
				return "claude"
			}
		}
	}

	// Check settings config
	settingsPath := filepath.Join(s.townRoot, "settings", "config.json")
	if data, err := os.ReadFile(settingsPath); err == nil {
		var cfg struct {
			DefaultAgent string            `json:"default_agent"`
			RoleAgents   map[string]string `json:"role_agents"`
		}
		if json.Unmarshal(data, &cfg) == nil {
			return cfg.DefaultAgent
		}
	}

	return "unknown"
}

// agentLogPaths returns candidate log files, preferring typescript for rig agents.
func agentLogPaths(townRoot, sessionName, rig, role, workerName string) []string {
	var paths []string
	if sessionName == "orchestrator" {
		return []string{filepath.Join(townRoot, "logs", "orchestrator.log")}
	}
	logDir := filepath.Join(townRoot, "logs", "sessions")
	paths = append(paths,
		filepath.Join(logDir, sessionName+".log"),
		filepath.Join(logDir, sessionName+".wrapper.log"),
	)
	if rig == "" {
		// Town-level orchestrated agents (hq-planner, hq-setup, …).
		switch role {
		case constants.RolePlanner:
			paths = append(paths, filepath.Join(townRoot, constants.DirPlanner, "typescript"))
		case constants.RoleSetup:
			paths = append(paths, filepath.Join(townRoot, constants.DirSetup, "typescript"))
		case constants.RoleMayor:
			paths = append(paths, filepath.Join(townRoot, constants.DirMayor, "typescript"))
		case constants.RoleDeacon:
			paths = append(paths, filepath.Join(townRoot, "deacon", "typescript"))
		case constants.RoleMechanic:
			paths = append(paths, filepath.Join(townRoot, constants.DirMechanic, "typescript"))
		}
		return paths
	}
	// Prefer logs/sessions/*.log (NATS wrapper / orchestrated gt-agent) over
	// typescript — script(1) transcripts often contain terminal escape noise.
	switch role {
	case "architect", "qa", "witness", "refinery":
		paths = append(paths, filepath.Join(townRoot, rig, role, "typescript"))
	case "polecat":
		if workerName != "" && workerName != sessionName && !strings.HasSuffix(sessionName, "-polecat") {
			paths = append(paths, filepath.Join(townRoot, rig, "polecats", workerName, "typescript"))
		} else {
			paths = append(paths, filepath.Join(townRoot, rig, "polecat", "typescript"))
		}
	case "crew":
		if workerName != "" && workerName != sessionName {
			paths = append(paths, filepath.Join(townRoot, rig, "crew", workerName, "typescript"))
		}
	}
	return paths
}

// readAgentLogs reads the last N lines from an agent's log sources.
func (s *Server) readAgentLogs(sessionName, rig, role, workerName string, n int) []string {
	for _, path := range agentLogPaths(s.townRoot, sessionName, rig, role, workerName) {
		if lines := tailFileLines(path, n); len(lines) > 0 {
			return lines
		}
	}
	return nil
}

func tailFileLines(path string, n int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

// AgentState holds persisted agent state.
type AgentState struct {
	PatrolCount         int       `json:"patrol_count"`
	IdleCycles          int       `json:"idle_cycles"`
	ExtraordinaryAction bool      `json:"extraordinary_action"`
	LastActivity        time.Time `json:"last_activity"`
}

// readAgentState reads the agent's state file.
func (s *Server) readAgentState(sessionName, rig, role, name string) AgentState {
	var state AgentState
	// Try to find state file in agent directory
	var statePath string
	if rig == "" {
		// Town-level agent: townRoot/<role>/gt-agent-state.json
		statePath = filepath.Join(s.townRoot, role, "gt-agent-state.json")
	} else {
		// Rig-level agent
		if role == "polecat" {
			statePath = filepath.Join(s.townRoot, rig, "polecats", name, "gt-agent-state.json")
		} else if role == "crew" {
			statePath = filepath.Join(s.townRoot, rig, "crew", name, "gt-agent-state.json")
		} else {
			// Rig singleton: townRoot/<rig>/<role>/gt-agent-state.json
			statePath = filepath.Join(s.townRoot, rig, role, "gt-agent-state.json")
		}
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		return state
	}
	json.Unmarshal(data, &state)
	return state
}

// agentIDToSessionName converts an agent ID to its session name.
// Town-level agents: mayor -> hq-mayor, deacon -> hq-deacon
// Rig agents: prefix-role -> prefix-role
func (s *Server) agentIDToSessionName(id string) string {
	if id == "orchestrator" {
		return "orchestrator"
	}
	// Town-level agents
	if id == "mayor" || id == "deacon" || id == "planner" || id == "setup" || id == "mechanic" {
		return "hq-" + id
	}
	return id
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
