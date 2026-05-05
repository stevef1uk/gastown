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
	"github.com/steveyegge/gastown/internal/nudge"
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
	Activity  string    `json:"activity,omitempty"`
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
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles))))
}

// handleAgents returns a JSON list of all agents.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	agents := s.discoverAgents()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agents)
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
	http.Error(w, "agent not found", http.StatusNotFound)
}

// handleSendMessage sends a nudge to an agent via NATS.
func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Use nudge.Enqueue directly — bypasses gt nudge session validation
	// which fails for NATS-based agents. The agent picks up queued nudges
	// via nudge.Drain() on its next cycle.
	sessionName := s.agentIDToSessionName(id)
	err := nudge.Enqueue(s.townRoot, sessionName, nudge.QueuedNudge{
		Sender:    "overseer (web console)",
		Message:   req.Message,
		Priority:  nudge.PriorityNormal,
		Timestamp: time.Now(),
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to queue nudge: %v", err), http.StatusInternalServerError)
		return
	}

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

	sessionName := s.agentIDToSessionName(id)
	logs := s.readAgentLogs(sessionName, 50)
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
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
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
			logs := s.readAgentLogs(s.agentIDToSessionName(id), 100)
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

	status := map[string]interface{}{
		"agents_total":    len(agents),
		"agents_running":  running,
		"nats_connected":  s.nc != nil && s.nc.IsConnected(),
		"town_root":       s.townRoot,
		"timestamp":       time.Now().Format(time.RFC3339),
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
	var agents []Agent

	// Check for town-level agents (mayor, deacon)
	for _, role := range []string{"mayor", "deacon"} {
		a := s.inspectAgent(role, "", role)
		if a.Status != "stopped" || fileExists(filepath.Join(s.townRoot, role)) {
			agents = append(agents, a)
		}
	}

	// Check for rig-level agents (witness, refinery, crew, polecats)
	rigsDir := filepath.Join(s.townRoot, "mayor", "rigs.json")
	if data, err := os.ReadFile(rigsDir); err == nil {
		var rigs struct {
			Rigs map[string]struct {
				Beads struct {
					Prefix string `json:"prefix"`
				} `json:"beads"`
			} `json:"rigs"`
		}
		if json.Unmarshal(data, &rigs) == nil {
			for rigName := range rigs.Rigs {
				for _, role := range []string{"witness", "refinery"} {
					id := fmt.Sprintf("%s-%s", rigs.Rigs[rigName].Beads.Prefix, role)
					a := s.inspectAgent(id, rigName, role)
					agents = append(agents, a)
				}
				// Check crew
				crewDir := filepath.Join(s.townRoot, rigName, "crew")
				if entries, err := os.ReadDir(crewDir); err == nil {
					for _, e := range entries {
						if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
							id := fmt.Sprintf("%s-crew-%s", rigs.Rigs[rigName].Beads.Prefix, e.Name())
							a := s.inspectAgent(id, rigName, "crew")
							a.Name = e.Name()
							agents = append(agents, a)
						}
					}
				}
				// Check polecats
				polecatDir := filepath.Join(s.townRoot, rigName, "polecats")
				if entries, err := os.ReadDir(polecatDir); err == nil {
					for _, e := range entries {
						if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
							id := fmt.Sprintf("%s-polecat-%s", rigs.Rigs[rigName].Beads.Prefix, e.Name())
							a := s.inspectAgent(id, rigName, "polecat")
							a.Name = e.Name()
							agents = append(agents, a)
						}
					}
				}
			}
		}
	}

	return agents
}

// inspectAgent checks the status of a specific agent.
func (s *Server) inspectAgent(id, rig, role string) Agent {
	a := Agent{
		ID:   id,
		Role: role,
		Name: id,
	}

	// Check if agent process is running
	var sessionName string
	if rig == "" {
		sessionName = fmt.Sprintf("hq-%s", role)
	} else {
		prefix := ""
		if data, err := os.ReadFile(filepath.Join(s.townRoot, "mayor", "rigs.json")); err == nil {
			var rigs struct {
				Rigs map[string]struct {
					Beads struct {
						Prefix string `json:"prefix"`
					} `json:"beads"`
				} `json:"rigs"`
			}
			if json.Unmarshal(data, &rigs) == nil {
				prefix = rigs.Rigs[rig].Beads.Prefix
			}
		}
		sessionName = fmt.Sprintf("%s-%s", prefix, role)
		if role == "polecat" || role == "crew" {
			parts := strings.Split(id, "-")
			if len(parts) >= 3 {
				sessionName = fmt.Sprintf("%s-%s-%s", prefix, role, strings.Join(parts[2:], "-"))
			}
		}
	}

	// Try to find the process
	pid := s.findAgentPID(sessionName)
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

	// Read agent type from config
	a.AgentType = s.detectAgentType(sessionName)

	// Read recent activity from logs
	logs := s.readAgentLogs(sessionName, 5)
	if len(logs) > 0 {
		a.Activity = logs[len(logs)-1]
	}

	// Read state file for more info
	state := s.readAgentState(sessionName)
	if state.PatrolCount > 0 {
		a.Activity = fmt.Sprintf("Patrol #%d", state.PatrolCount)
	}

	return a
}

// findAgentPID finds the PID of an agent by its session name.
// For NATS-based gt-agent processes, the session name is NOT in the
// command line (it's passed via env var). We must search for gt-agent
// processes and match them by role name in the title string.
func (s *Server) findAgentPID(sessionName string) int {
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

	// Fallback 1: search for gt-agent processes matching the role name
	// in the process title. The title contains the role (mayor, deacon,
	// witness, refinery) but NOT the session prefix.
	role := sessionName
	if strings.HasPrefix(sessionName, "hq-") {
		role = strings.TrimPrefix(sessionName, "hq-")
	} else if strings.Contains(sessionName, "-") {
		// Rig agents: de-witness -> witness, de-refinery -> refinery
		parts := strings.Split(sessionName, "-")
		if len(parts) >= 2 {
			role = parts[1]
		}
	}

	// Search for gt-agent processes with this role in the title
	searchTerm := fmt.Sprintf("gt-agent.*%s", role)
	cmd := exec.Command("pgrep", "-a", "-f", searchTerm)
	out, _ := cmd.Output()
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		// Verify this process's cwd is inside the town root
		// (prevents matching gt-agent processes from other towns)
		cwd, err := os.Readlink(filepath.Join("/proc", fields[0], "cwd"))
		if err != nil {
			continue
		}
		if strings.HasPrefix(cwd, s.townRoot) {
			return pid
		}
	}
	return 0
}

// detectAgentType determines what agent binary is used.
func (s *Server) detectAgentType(sessionName string) string {
	// Check wrapper log for agent type
	logFile := filepath.Join(s.townRoot, "logs", "sessions", sessionName+".wrapper.log")
	if data, err := os.ReadFile(logFile); err == nil {
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

// readAgentLogs reads the last N lines from an agent's wrapper log.
func (s *Server) readAgentLogs(sessionName string, n int) []string {
	logFile := filepath.Join(s.townRoot, "logs", "sessions", sessionName+".wrapper.log")
	data, err := os.ReadFile(logFile)
	if err != nil {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	// Trim empty lines
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
func (s *Server) readAgentState(sessionName string) AgentState {
	var state AgentState
	// Try to find state file in agent directory
	var statePath string
	if strings.HasPrefix(sessionName, "hq-") {
		role := strings.TrimPrefix(sessionName, "hq-")
		statePath = filepath.Join(s.townRoot, role, "gt-agent-state.json")
	} else {
		// For rig agents, need to parse session name
		statePath = filepath.Join(s.townRoot, "gt-agent-state.json")
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
	// Town-level agents
	if id == "mayor" || id == "deacon" {
		return "hq-" + id
	}
	return id
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
