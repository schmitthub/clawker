package config

import (
	"fmt"
	"testing"
)

func TestLoadActualConfig(t *testing.T) {
	loader := NewLoader("/Users/andrew/Code/clawker")
	cfg, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}

	fmt.Printf("Build.Instructions: %+v\n", cfg.Build.Instructions)
	if cfg.Build.Instructions != nil {
		fmt.Printf("RootRun count: %d\n", len(cfg.Build.Instructions.RootRun))
		for i, r := range cfg.Build.Instructions.RootRun {
			fmt.Printf("  [%d] Cmd: %q\n", i, r.Cmd[:min(50, len(r.Cmd))])
		}
	} else {
		fmt.Println("Instructions is NIL!")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
