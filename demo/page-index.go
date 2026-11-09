package demo

import (
	"log/slog"

	"github.com/go-app-blazar/blazar/blazar"
	"github.com/maxence-charriere/go-app/v11/pkg/app"
)

type IndexPage struct {
	app.Compo
}

func (c *IndexPage) OnMount(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: OnMount")
}

func (c *IndexPage) OnNav(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: OnNav")
}

func (c *IndexPage) Render() app.UI {
	return blazar.Page().
		Body(
			app.P().
				Body(
					app.Text("This is a collection of tools for analyzing the tax parcels in New Castle County."),
				),
		)
}
