package orchestrator

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/steveyegge/gastown/internal/townlog"
)

var planningTriadInFlight sync.Map

func schedulePlanningTriadAdvisory(townRoot, rigDir, fromState string, cfg TriadValidationConfig) {
	key := struct {
		rigDir string
		digest [sha256.Size]byte
	}{rigDir, sha256.Sum256([]byte(cfg.SPEC + "\x00" + cfg.Architecture + "\x00" + cfg.Plan))}
	if _, loaded := planningTriadInFlight.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	client := newJudgeClient(120 * time.Second)
	go func() {
		defer planningTriadInFlight.Delete(key)
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		pass, reason, err := ValidateTriadWithJudge(ctx, client, cfg)
		if err != nil {
			log.Printf("[triad] background advisory for %s (%s) unavailable: %v", rigDir, fromState, err)
		} else if !pass {
			message := fmt.Sprintf("SPEC/Architecture/Plan snapshot misaligned for %s (%s): %s", rigDir, fromState, reason)
			log.Printf("[triad] advisory: %s", message)
			if townRoot != "" {
				tl := townlog.NewLogger(townRoot)
				_ = tl.Log(townlog.EventType("advisory"), "orchestrator", message)
			}
		}
	}()
}
