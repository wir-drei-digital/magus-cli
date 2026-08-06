package cli

import "testing"

func TestNewACPCmd(t *testing.T) {
	cmd := newACPCmd()
	if cmd.Use != "acp" {
		t.Errorf("Use = %q, want acp", cmd.Use)
	}
	if cmd.RunE == nil {
		t.Error("acp command must have a RunE")
	}
}

func TestACPCmdRegistered(t *testing.T) {
	root := newRootCmd()
	var found bool
	for _, c := range root.Commands() {
		if c.Name() == "acp" {
			found = true
			if c.GroupID != "agent" {
				t.Errorf("acp group = %q, want agent", c.GroupID)
			}
		}
	}
	if !found {
		t.Error("acp command not registered on root")
	}
}
