// Package beads provides progress bead operations for workflow tracking.
package beads

import (
	"fmt"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/constants"
)

// ProgressBeadTitle returns the well-known title for a task's progress bead.
func ProgressBeadTitle(taskID string) string {
	return "Progress: " + taskID
}

// FindProgressBead finds the progress bead for a task by title.
// Returns nil if not found (not an error).
func (b *Beads) FindProgressBead(taskID string) (*Issue, error) {
	issues, err := b.List(ListOptions{Label: "gt:progress", Priority: -1})
	if err != nil {
		return nil, fmt.Errorf("listing progress issues: %w", err)
	}

	targetTitle := ProgressBeadTitle(taskID)
	for _, issue := range issues {
		if issue.Title == targetTitle {
			return issue, nil
		}
	}

	return nil, nil
}

// GetOrCreateProgressBead returns the progress bead for a task, creating it if needed.
func (b *Beads) GetOrCreateProgressBead(taskID, initialStatus string) (*Issue, error) {
	// Check if it exists
	existing, err := b.FindProgressBead(taskID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	// Validate status
	validStatuses := constants.BeadsCustomStatusesList()
	isValid := false
	for _, status := range validStatuses {
		if status == initialStatus {
			isValid = true
			break
		}
	}
	if !isValid {
		return nil, fmt.Errorf("invalid progress status %q, valid statuses: %s", initialStatus, strings.Join(validStatuses, ", "))
	}

	issue, err := b.Create(CreateOptions{
		Title:       ProgressBeadTitle(taskID),
		Labels:      []string{"gt:progress"},
		Priority:    3, // P3 - normal priority for progress tracking
		Description: fmt.Sprintf("Progress tracking for task %s\n\nStarted: %s", taskID, time.Now().Format(time.RFC3339)),
		Actor:       "system",
	})
	if err != nil {
		return nil, fmt.Errorf("creating progress bead: %w", err)
	}

	// Set initial status
	if err := b.Update(issue.ID, UpdateOptions{Status: &initialStatus}); err != nil {
		// Best-effort cleanup
		_ = b.CloseWithReason("orphaned: failed to set initial status", issue.ID)
		return nil, fmt.Errorf("setting progress bead initial status: %w", err)
	}

	// Re-fetch to get updated status
	return b.Show(issue.ID)
}

// UpdateProgressStatus updates the status of a progress bead.
// Creates the progress bead if it doesn't exist.
func (b *Beads) UpdateProgressStatus(taskID, newStatus string) error {
	progressBead, err := b.FindProgressBead(taskID)
	if err != nil {
		return fmt.Errorf("finding progress bead: %w", err)
	}

	// Create progress bead if it doesn't exist
	if progressBead == nil {
		progressBead, err = b.GetOrCreateProgressBead(taskID, newStatus)
		if err != nil {
			return fmt.Errorf("creating progress bead: %w", err)
		}
		return nil // Already set to newStatus during creation
	}

	// Validate status
	validStatuses := constants.BeadsCustomStatusesList()
	isValid := false
	for _, status := range validStatuses {
		if status == newStatus {
			isValid = true
			break
		}
	}
	if !isValid {
		return fmt.Errorf("invalid progress status %q, valid statuses: %s", newStatus, strings.Join(validStatuses, ", "))
	}

	// Update status
	if err := b.Update(progressBead.ID, UpdateOptions{Status: &newStatus}); err != nil {
		return fmt.Errorf("updating progress status: %w", err)
	}

	return nil
}

// GetProgressStatus returns the current status of a task's progress bead.
func (b *Beads) GetProgressStatus(taskID string) (string, error) {
	progressBead, err := b.FindProgressBead(taskID)
	if err != nil {
		return "", fmt.Errorf("finding progress bead: %w", err)
	}
	if progressBead == nil {
		return "", fmt.Errorf("progress bead not found for task %s", taskID)
	}

	return progressBead.Status, nil
}
