package main

import (
	"game-server/internal/app"

	"go.uber.org/fx"
)

// Main entry point for the game server
func main() {
	fx.New(
		app.Module,
		fx.NopLogger,
	).Run()
}
