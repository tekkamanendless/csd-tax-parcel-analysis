package demo

import (
	"github.com/go-app-blazar/blazar/blazar"
	"github.com/go-app-blazar/router"
	"github.com/maxence-charriere/go-app/v11/pkg/app"
)

func Routes() []router.Route {
	return []router.Route{
		{
			Path: "/",
			Component: func() app.Composer {
				return blazar.MainLayout().
					HeadlineText("CSD Tax Parcel Tools").
					DrawerFunction(func() app.UI {
						return app.Div().
							Body(
								blazar.Item().
									Label("Home").
									To("/"),
								blazar.Item().
									Label("2026 Tax Simulator").
									To("/2026-tax-simulator"),
								blazar.Item().
									Label("2026 Tax History").
									To("/2026-tax-history"),
							)
					})
			},
			Children: []router.Route{
				{
					Path: "/",
					Component: func() app.Composer {
						return &IndexPage{}
					},
				},
				{
					Path: "/2026-tax-simulator",
					Component: func() app.Composer {
						return &Simulator2026Page{}
					},
				},
				{
					Path: "/2026-tax-history",
					Component: func() app.Composer {
						return &TaxHistoryPage{}
					},
				},
			},
		},
	}
}
