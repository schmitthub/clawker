package main

import (
	"os"

	"github.com/schmitthub/clawker/internal/controlplane"
)

func main() {
	os.Exit(controlplane.Main())
}
