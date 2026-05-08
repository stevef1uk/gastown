// Package session provides polecat session lifecycle management.
package session

import (
	"fmt"
)

// DefaultPrefix is the default beads prefix used when no rig-specific prefix is known.
const DefaultPrefix = "gt"

// HQPrefix is the prefix for town-level services (Mayor, Deacon).
const HQPrefix = "hq-"

// MayorSessionName returns the session name for the Mayor agent.
// One mayor per machine - multi-town requires containers/VMs for isolation.
func MayorSessionName() string {
	return HQPrefix + "mayor"
}

// DeaconSessionName returns the session name for the Deacon agent.
// One deacon per machine - multi-town requires containers/VMs for isolation.
func DeaconSessionName() string {
	return HQPrefix + "deacon"
}

// PlannerSessionName returns the session name for the Planner agent.
// One planner per machine - multi-town requires containers/VMs for isolation.
func PlannerSessionName() string {
	return HQPrefix + "planner"
}

// MechanicSessionName returns the session name for the Mechanic agent.
// One mechanic per machine - multi-town requires containers/VMs for isolation.
func MechanicSessionName() string {
	return HQPrefix + "mechanic"
}

// MechanicSessionNameForRig returns the session name for a rig's Mechanic agent.
func MechanicSessionNameForRig(rigName string) string {
	prefix := PrefixFor(rigName)
	if rigName == "" || rigName == prefix {
		return fmt.Sprintf("%s-mechanic", prefix)
	}
	return fmt.Sprintf("%s-%s-mechanic", prefix, rigName)
}

// WitnessSessionName returns the session name for a rig's Witness agent.
// rigPrefix is the rig's beads prefix (e.g., "gt" for gastown, "bd" for beads).
// rigName is the name of the rig (e.g., "testgt1").
func WitnessSessionName(rigPrefix, rigName string) string {
	prefix := rigPrefix
	if rigName == "" || rigName == rigPrefix {
		return fmt.Sprintf("%s-witness", prefix)
	}
	return fmt.Sprintf("%s-%s-witness", prefix, rigName)
}

// WitnessSessionNameForRig returns the session name for a rig's Witness agent by rig name.
func WitnessSessionNameForRig(rigName string) string {
	return WitnessSessionName(PrefixFor(rigName), rigName)
}

// RefinerySessionName returns the session name for a rig's Refinery agent.
// rigPrefix is the rig's beads prefix (e.g., "gt" for gastown, "bd" for beads).
// rigName is the name of the rig (e.g., "testgt1").
func RefinerySessionName(rigPrefix, rigName string) string {
	prefix := rigPrefix
	if rigName == "" || rigName == rigPrefix {
		return fmt.Sprintf("%s-refinery", prefix)
	}
	return fmt.Sprintf("%s-%s-refinery", prefix, rigName)
}

// RefinerySessionNameForRig returns the session name for a rig's Refinery agent by rig name.
func RefinerySessionNameForRig(rigName string) string {
	return RefinerySessionName(PrefixFor(rigName), rigName)
}

// ArchitectSessionName returns the session name for a rig's Architect agent.
func ArchitectSessionName(rigPrefix, rigName string) string {
	prefix := rigPrefix
	if rigName == "" || rigName == rigPrefix {
		return fmt.Sprintf("%s-architect", prefix)
	}
	return fmt.Sprintf("%s-%s-architect", prefix, rigName)
}

// QASessionName returns the session name for a rig's QA agent.
func QASessionName(rigPrefix, rigName string) string {
	prefix := rigPrefix
	if rigName == "" || rigName == rigPrefix {
		return fmt.Sprintf("%s-qa", prefix)
	}
	return fmt.Sprintf("%s-%s-qa", prefix, rigName)
}

// CrewSessionName returns the session name for a crew worker in a rig.
// rigPrefix is the rig's beads prefix (e.g., "gt" for gastown, "bd" for beads).
func CrewSessionName(rigPrefix, name string) string {
	return fmt.Sprintf("%s-crew-%s", rigPrefix, name)
}

// PolecatSessionName returns the session name for a polecat in a rig.
// rigPrefix is the rig's beads prefix (e.g., "gt" for gastown, "bd" for beads).
func PolecatSessionName(rigPrefix, name string) string {
	return fmt.Sprintf("%s-%s", rigPrefix, name)
}

// OverseerSessionName returns the session name for the human operator.
// The overseer is the human who controls Gas Town, not an AI agent.
func OverseerSessionName() string {
	return HQPrefix + "overseer"
}

// BootSessionName returns the session name for the Boot watchdog.
// Boot is town-level (launched by deacon), so it uses the hq- prefix.
// "hq-boot" avoids tmux prefix-matching collisions with "hq-deacon".
func BootSessionName() string {
	return HQPrefix + "boot"
}

// DogSessionName returns the session name for a named dog agent.
// Dogs are town-level (managed by deacon), so they use the hq- prefix.
// Pattern: hq-dog-<name> (e.g., hq-dog-alpha).
func DogSessionName(name string) string {
	return fmt.Sprintf("%sdog-%s", HQPrefix, name)
}
