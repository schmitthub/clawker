package main

import (
	"fmt"
	"os"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/controlplane/firewall"
	"gopkg.in/yaml.v3"
)

type rulesFile struct {
	Rules []config.EgressRule `yaml:"rules"`
}

func main() {
	rulesPath := "/Users/andrew/Code/clawker/.clawkerlocal/.local/share/clawker/firewall/egress-rules.yaml"
	outPath := "/Users/andrew/Code/clawker/.clawkerlocal/.local/share/clawker/firewall/envoy.yaml"
	data, err := os.ReadFile(rulesPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var rf rulesFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ports := firewall.EnvoyPorts{EgressPort: 10000, TCPPortBase: 10001, HealthPort: 18901}
	yamlBytes, _, err := firewall.GenerateEnvoyConfig(rf.Rules, ports, firewall.ALSConfig{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, yamlBytes, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %d bytes (%d rules)\n", len(yamlBytes), len(rf.Rules))
}
