package main

import (
	"3dtest-server/internal/app"

	"go.uber.org/fx"
)

func main() {
	fx.New(
		app.Module,
	).Run()
}
