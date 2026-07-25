package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const instancesFileName = "instances.json"

// InstancesPath returns the path to the persisted workflow instances file.
func InstancesPath(townRoot string) string {
	return filepath.Join(townRoot, "orchestrator", instancesFileName)
}

// EnsureInstancesDir creates orchestrator/ under town root so atomic persist cannot fail on rename.
func EnsureInstancesDir(townRoot string) error {
	if strings.TrimSpace(townRoot) == "" {
		return nil
	}
	return os.MkdirAll(filepath.Dir(InstancesPath(townRoot)), 0755)
}

type instancesSnapshot struct {
	Instances []*WorkflowInstance `json:"instances"`
	NextSeq   int                 `json:"next_seq"`
}

// LoadInstancesSnapshot reads persisted workflow instances without a Manager.
func LoadInstancesSnapshot(townRoot string) (*instancesSnapshot, error) {
	path := InstancesPath(townRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &instancesSnapshot{}, nil
		}
		return nil, err
	}
	var snap instancesSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &snap, nil
}

// LoadInstances restores workflow instances from disk into the manager.
func (m *Manager) LoadInstances() error {
	snap, err := LoadInstancesSnapshot(m.townRoot)
	if err != nil {
		return err
	}
	if snap == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.instances = make(map[string]*WorkflowInstance, len(snap.Instances))
	for _, inst := range snap.Instances {
		if inst == nil || inst.ID == "" {
			continue
		}
		copy := *inst
		if copy.Variables == nil {
			copy.Variables = map[string]string{}
		}
		m.instances[copy.ID] = &copy
	}
	m.nextSeq = snap.NextSeq
	if m.nextSeq <= 0 {
		m.nextSeq = maxInstanceSeq(m.instances)
	}
	return nil
}

func maxInstanceSeq(instances map[string]*WorkflowInstance) int {
	max := 0
	for id := range instances {
		if !strings.HasPrefix(id, "wf-") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(id, "wf-"))
		if err == nil && n > max {
			max = n
		}
	}
	return max
}

func (m *Manager) persistLocked() error {
	if err := EnsureInstancesDir(m.townRoot); err != nil {
		return err
	}
	snap := instancesSnapshot{
		Instances: make([]*WorkflowInstance, 0, len(m.instances)),
		NextSeq:   m.nextSeq,
	}
	for _, inst := range m.instances {
		copy := *inst
		if copy.Variables == nil {
			copy.Variables = map[string]string{}
		}
		snap.Instances = append(snap.Instances, &copy)
	}

	path := InstancesPath(m.townRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, instancesFileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp: %w", err)
	}
	f.Close()
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

func (m *Manager) allocateWorkflowID() string {
	if m.nextSeq <= 0 {
		m.nextSeq = maxInstanceSeq(m.instances)
	}
	m.nextSeq++
	return fmt.Sprintf("wf-%d", m.nextSeq)
}
