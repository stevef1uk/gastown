package cmd

import "testing"

func TestFeedConvoysFlagRegistered(t *testing.T) {
	flag := feedCmd.Flags().Lookup("convoys")
	if flag == nil {
		t.Fatal("feed --convoys flag is not registered")
	}
	if flag.DefValue != "false" {
		t.Fatalf("feed --convoys default = %q, want false", flag.DefValue)
	}
}
