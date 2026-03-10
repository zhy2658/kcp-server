package main

import (
	"game-server/internal/app"

	"go.uber.org/fx"
)

func main() {
	fx.New(
		app.Module,
		fx.NopLogger,
	).Run()
}
