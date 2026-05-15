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

type instancesSnapshot struct {
	Instances []*WorkflowInstance `json:"instances"`
	NextSeq   int                 `json:"next_seq"`
}

// LoadInstances restores workflow instances from disk into the manager.
func (m *Manager) LoadInstances() error {
	path := InstancesPath(m.townRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var snap instancesSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
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
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (m *Manager) allocateWorkflowID() string {
	if m.nextSeq <= 0 {
		m.nextSeq = maxInstanceSeq(m.instances)
	}
	m.nextSeq++
	return fmt.Sprintf("wf-%d", m.nextSeq)
}
