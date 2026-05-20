package main

import "testing"

func TestDockerVerifyCommandMatches_mayorRigCd(t *testing.T) {
	verify := "cd . && docker-compose -f docker-compose.yml config"
	cmd := "cd finally/mayor/rig && docker-compose -f docker-compose.yml config"
	if !dockerVerifyCommandMatches(cmd, verify) {
		t.Fatal("expected mayor/rig verify to match layout hint")
	}
}
