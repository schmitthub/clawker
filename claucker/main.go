package main

import (
	"os"

	"github.com/claucker/claucker/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
