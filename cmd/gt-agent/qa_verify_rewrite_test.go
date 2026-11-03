package main

import (
	"errors"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestRewrittenPhaseVerifyTracking(t *testing.T) {
	for _, track := range []string{"qa", "test_review"} {
		for _, failed := range []bool{false, true} {
			name := track + "/success"
			if failed {
				name = track + "/failure"
			}
			t.Run(name, func(t *testing.T) {
				town, rig, v := flatGoAPISmokeTestRig(t)
				verify := "cd helloapi && go run . & sleep 2 && curl -s http://127.0.0.1:8080/hello && kill $(lsof -ti:8080)"
				v.DeliveryPhases[0].QAVerifyCommand = verify
				r := &stateRunner{
					townRoot: town, rig: rig, v: v,
					hooks: orchestrator.StateHooks{CmdGuard: "qa", Track: track},
					track: &cmdTracker{},
				}
				cmd := r.rewriteCommand("cd " + rigMayorRigPath(rig) + " && " + verify)
				if !orchestrator.IsProfileDerivedSmokeCommand(cmd) {
					t.Fatalf("expected managed smoke rewrite, got %q", cmd)
				}
				if isQATestCommandOK(cmd, v) {
					t.Fatal("fixture must reproduce verification identity lost during rewriting")
				}
				if r.track.unittestOK || r.track.verifyOK {
					t.Fatal("rewriting must not count as successful execution")
				}
				var cmdErr error
				if failed {
					cmdErr = errors.New("smoke failed")
				}
				r.trackCommand(cmd, cmdErr)
				got := r.track.unittestOK
				if track == "test_review" {
					got = r.track.verifyOK
				}
				if got != !failed {
					t.Fatalf("verification credited = %v, failed = %v", got, failed)
				}
				if track == "qa" && r.track.qaSmokeOK != !failed {
					t.Fatalf("runtime smoke credited = %v, failed = %v", r.track.qaSmokeOK, failed)
				}
			})
		}
	}
}

func TestSmokeDoesNotReplacePhaseTests(t *testing.T) {
	town, rig, v := flatGoAPISmokeTestRig(t)
	r := &stateRunner{
		townRoot: town, rig: rig, v: v,
		hooks: orchestrator.StateHooks{CmdGuard: "qa", Track: "qa"},
		track: &cmdTracker{},
	}
	cmd := r.rewriteCommand("cd " + rigMayorRigPath(rig) + "/helloapi && go run .")
	r.trackCommand(cmd, nil)
	if !r.track.qaSmokeOK || r.track.unittestOK {
		t.Fatalf("smoke must not satisfy separate go test requirement: %+v", r.track)
	}
}

func TestRewrittenVerifyDoesNotSurvivePhaseChange(t *testing.T) {
	town, rig, v := flatGoAPISmokeTestRig(t)
	verify := "cd helloapi && go run . & sleep 2 && curl -s http://127.0.0.1:8080/hello && kill $(lsof -ti:8080)"
	v.DeliveryPhases[0].QAVerifyCommand = verify
	r := &stateRunner{
		townRoot: town, rig: rig, v: v,
		hooks: orchestrator.StateHooks{CmdGuard: "qa", Track: "qa"},
		track: &cmdTracker{},
	}
	cmd := r.rewriteCommand("cd " + rigMayorRigPath(rig) + " && " + verify)
	r.v.DeliveryPhases[0].QAVerifyCommand = "cd helloapi && go test ./..."
	r.trackCommand(cmd, nil)
	if r.track.unittestOK {
		t.Fatal("a previous phase's rewritten verify must not satisfy current phase tests")
	}
}
