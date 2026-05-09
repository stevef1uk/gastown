package beads

import (
	"fmt"
	"strings"
)

type Resolver struct {
	beads *Beads
}

func NewResolver(b *Beads) *Resolver {
	return &Resolver{beads: b}
}

// ResolveID converts a shorthand ID or title fragment into a full bead name.
// It prioritizes exact matches, then prefix matches, then substring matches.
// By default, it includes ephemeral (wisp) beads in the search.
func (r *Resolver) ResolveID(id string) (string, error) {
	return r.ResolveIDWithOptions(id, ListOptions{Ephemeral: true})
}

// ResolveIDWithOptions allows passing ListOptions during resolution.
// This is important for finding ephemeral (wisp) beads when needed.
func (r *Resolver) ResolveIDWithOptions(id string, opts ListOptions) (string, error) {
	if id == "" {
		return "", fmt.Errorf("empty ID")
	}

	// Check if it's already a full bead name
	if _, err := r.beads.Show(id); err == nil {
		return id, nil
	}

	// For ephemeral search, we need to search both regular issues and wisps
	if opts.Ephemeral {
		// First try searching regular issues
		results, err := r.beads.List(opts)
		if err != nil {
			return "", fmt.Errorf("listing regular issues: %w", err)
		}
		
		if len(results) == 0 {
			// If no results in regular issues, try wisps (ephemeral)
			wispOpts := opts
			wispOpts.Ephemeral = true
			results, err = r.beads.List(wispOpts)
			if err != nil {
				return "", fmt.Errorf("listing ephemeral issues: %w", err)
			}
		}
		
		if len(results) == 0 {
			return "", fmt.Errorf("no bead matches ID %q", id)
		}
		
		if len(results) > 1 {
			// If we have multiple matches, check if any of them are EXACT matches for the ID suffix
			var exactMatches []string
			for _, b := range results {
				if b.ID == id || strings.HasSuffix(b.ID, "-"+id) {
					exactMatches = append(exactMatches, b.ID)
				}
			}
			
			if len(exactMatches) == 1 {
				return exactMatches[0], nil
			}

			var names []string
			for _, b := range results {
				names = append(names, b.ID)
			}
			return "", fmt.Errorf("resolving ID %s: ambiguous ID %q matches %d issues: %v\nUse more characters to disambiguate", id, id, len(results), names)
		}

		return results[0].ID, nil
	}

	// Non-ephemeral search
	results, err := r.beads.List(opts)
	if err != nil {
		return "", fmt.Errorf("listing issues: %w", err)
	}

	if len(results) == 0 {
		return "", fmt.Errorf("no bead matches ID %q", id)
	}

	if len(results) > 1 {
		// If we have multiple matches, check if any of them are EXACT matches for the ID suffix
		var exactMatches []string
		for _, b := range results {
			if b.ID == id || strings.HasSuffix(b.ID, "-"+id) {
				exactMatches = append(exactMatches, b.ID)
			}
		}
		
		if len(exactMatches) == 1 {
			return exactMatches[0], nil
		}

		var names []string
		for _, b := range results {
			names = append(names, b.ID)
		}
		return "", fmt.Errorf("resolving ID %s: ambiguous ID %q matches %d issues: %v\nUse more characters to disambiguate", id, id, len(results), names)
	}

	return results[0].ID, nil
}
