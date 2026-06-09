package session

// NatsAutoRespawnAllowed, when set by the daemon at init, gates NATS auto-respawn for
// orchestrated pipeline sessions (rig workflow must be running). Patrol agents use
// the default allow when nil or when the gate returns true.
var NatsAutoRespawnAllowed func(townRoot, sessionID string) bool

func natsSessionShouldRespawn(townRoot, sessionID string) bool {
	if NatsAutoRespawnAllowed == nil {
		return true
	}
	return NatsAutoRespawnAllowed(townRoot, sessionID)
}
