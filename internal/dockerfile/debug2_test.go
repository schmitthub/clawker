package dockerfile_test

import (
	"fmt"
	"github.com/schmitthub/claucker/internal/config"
	"github.com/schmitthub/claucker/internal/dockerfile"
	"strings"
	"testing"
)

func TestGenerateWithRealConfig(t *testing.T) {
	loader := config.NewLoader("/Users/andrew/Code/claucker")
	cfg, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}

	fmt.Printf("Instructions: %+v\n", cfg.Build.Instructions)

	gen := dockerfile.NewGenerator(cfg, "/Users/andrew/Code/claucker")
	df, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}

	content := string(df)

	// Check if Go install command is in the Dockerfile
	if strings.Contains(content, "go.dev") {
		fmt.Println("✓ Go installation command FOUND in Dockerfile")
	} else {
		fmt.Println("✗ Go installation command NOT FOUND in Dockerfile")
	}

	// Print the section around RUN commands
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "RUN") {
			fmt.Printf("Line %d: %s\n", i+1, line[:min(80, len(line))])
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
