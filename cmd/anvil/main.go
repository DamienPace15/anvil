package main

import (
	"os"

	"github.com/DamienPace15/anvil/cmd/anvil/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
