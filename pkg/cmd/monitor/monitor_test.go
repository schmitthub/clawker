package monitor

import (
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

func TestNewCmdMonitor(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdMonitor(f)

	if cmd.Use != "monitor" {
		t.Errorf("expected Use 'monitor', got '%s'", cmd.Use)
	}

	// Check subcommands are registered
	subcommands := []string{"init", "up", "down", "status"}
	for _, name := range subcommands {
		found := false
		for _, sub := range cmd.Commands() {
			if sub.Use == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected subcommand '%s' to be registered", name)
		}
	}
}

func TestNewCmdMonitorInit(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdMonitor(f)

	// Find init subcommand
	var initCmd *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Use == "init" {
			initCmd = sub
			break
		}
	}
	if initCmd == nil {
		t.Fatal("expected init subcommand to exist")
	}

	// Check flags
	forceFlag := initCmd.Flags().Lookup("force")
	if forceFlag == nil {
		t.Error("expected --force flag to exist")
	}
	if forceFlag.Shorthand != "f" {
		t.Errorf("expected --force shorthand 'f', got '%s'", forceFlag.Shorthand)
	}
	if forceFlag.DefValue != "false" {
		t.Errorf("expected --force default 'false', got '%s'", forceFlag.DefValue)
	}
}

func TestNewCmdMonitorUp(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdMonitor(f)

	// Find up subcommand
	var upCmd *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Use == "up" {
			upCmd = sub
			break
		}
	}
	if upCmd == nil {
		t.Fatal("expected up subcommand to exist")
	}

	// Check flags
	detachFlag := upCmd.Flags().Lookup("detach")
	if detachFlag == nil {
		t.Error("expected --detach flag to exist")
	}
	if detachFlag.Shorthand != "" {
		t.Errorf("expected --detach no shorthand, got '%s'", detachFlag.Shorthand)
	}
	if detachFlag.DefValue != "true" {
		t.Errorf("expected --detach default 'true', got '%s'", detachFlag.DefValue)
	}
}

func TestNewCmdMonitorDown(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdMonitor(f)

	// Find down subcommand
	var downCmd *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Use == "down" {
			downCmd = sub
			break
		}
	}
	if downCmd == nil {
		t.Fatal("expected down subcommand to exist")
	}

	// Check flags
	volumesFlag := downCmd.Flags().Lookup("volumes")
	if volumesFlag == nil {
		t.Error("expected --volumes flag to exist")
	}
	if volumesFlag.Shorthand != "v" {
		t.Errorf("expected --volumes shorthand 'v', got '%s'", volumesFlag.Shorthand)
	}
	if volumesFlag.DefValue != "false" {
		t.Errorf("expected --volumes default 'false', got '%s'", volumesFlag.DefValue)
	}
}

func TestNewCmdMonitorStatus(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdMonitor(f)

	// Find status subcommand
	var statusCmd *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Use == "status" {
			statusCmd = sub
			break
		}
	}
	if statusCmd == nil {
		t.Fatal("expected status subcommand to exist")
	}

	// Status has no flags - just verify it exists and Use is correct
	if statusCmd.Use != "status" {
		t.Errorf("expected Use 'status', got '%s'", statusCmd.Use)
	}
}
