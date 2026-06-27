package simplechart

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/maxence-charriere/go-app/v11/pkg/app"
)

func SimpleChart() *simpleChart {
	return &simpleChart{}
}

type simpleChart struct {
	app.Compo
	ILabel string
	IItems []SimpleChartItem
}

var _ app.Composer = (*simpleChart)(nil)
var _ app.Updater = (*simpleChart)(nil)

type SimpleChartItem struct {
	Label string
	Value float64
	Color string
}

func (c *simpleChart) OnUpdate(ctx app.Context) {
	if debugSimpleChart {
		slog.DebugContext(ctx.Context, "SimpleChart: OnUpdate")
	}
}

func (c *simpleChart) Label(label string) *simpleChart {
	c.ILabel = label
	return c
}

func (c *simpleChart) Items(items []SimpleChartItem) *simpleChart {
	c.IItems = items
	return c
}

func (c *simpleChart) Render() app.UI {
	if debugSimpleChart {
		slog.DebugContext(context.TODO(), "SimpleChart: Render", "ILabel", c.ILabel, "IItems", c.IItems)
	}

	dataSets := map[string]any{}
	for i, item := range c.IItems {
		dataSets[fmt.Sprintf("percentage-%d", i+1)] = item.Value
		if item.Color != "" {
			dataSets[fmt.Sprintf("color-%d", i+1)] = item.Color
		}
	}

	return app.Figure().
		Class("simplechart").
		Body(
			app.FigCaption().
				Class("simplechart__label").
				Text(c.ILabel),
			app.Ul().
				Class("simplechart__chart", "pie").
				DataSets(dataSets).
				Body(
					app.Range(c.IItems).Slice(func(i int) app.UI {
						item := c.IItems[i]

						dataSets := map[string]any{
							fmt.Sprintf("label-%d", i+1): "",
						}
						if item.Color != "" {
							dataSets["color"] = item.Color
						}

						return app.Li().
							DataSets(dataSets).
							Body(
								app.Span().Text(item.Label),
							)
					}),
				),
		)
}
