package demo

import (
	"context"
	"encoding/csv"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/go-app-blazar/blazar/blazar"
	"github.com/maxence-charriere/go-app/v11/pkg/app"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/tekkamanendless/csd-tax-parcel-analysis/dataset"
	"github.com/tekkamanendless/csd-tax-parcel-analysis/internal/database"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"gorm.io/gorm"
)

type IndexPage struct {
	app.Compo

	db              *gorm.DB
	schoolDistricts []string
	taxRates        []TaxRate

	selectedSchoolDistrict string

	maximumRateMultiplier float64

	loading                      bool
	residentialRate              float64
	nonResidentialRate           float64
	nonResidentialRateMultiplier float64

	propertyClassResults2024 []PropertyClassResult
	propertyClassResults2025 []PropertyClassResult
	totalTax                 float64

	calculating                          bool
	proposedResidentialRate              float64
	proposedNonResidentialRate           float64
	proposedNonResidentialRateMultiplier float64
	proposedApartmentClass               string
	proposedSpecialApartmentMultiplier   float64

	proposedPropertyClassResults []PropertyClassResult
	proposedTotalTax             float64
}

var printer *message.Printer = message.NewPrinter(language.English)

func (c *IndexPage) query(query string) string {
	var queryPrefix string
	{
		residentialPropertyClasses := []string{
			"farmland",
			"residential",
		}

		// countyrates AS (SELECT 0.21 AS votech2024, 0.0431 AS votech2025),

		var districtRateClause string
		{
			var parts []string
			for _, districtRate := range c.taxRates {
				parts = append(parts, fmt.Sprintf("SELECT '%s' AS district, %f AS residential_rate, %f AS non_residential_rate", districtRate.SchoolDistrict, districtRate.ResidentialRate, districtRate.NonResidentialRate))
			}
			districtRateClause = strings.Join(parts, " UNION ")

			districtRateClause = "districtrate AS (" + strings.TrimSpace(districtRateClause) + ")"
		}

		queryPrefix = "WITH\n" + districtRateClause + ",\n"
		queryPrefix += `parceltax AS ( SELECT parcel.parcelid AS parcelid, CAST(100 * parcel.school_taxable * CASE WHEN parcel.property_class IN ('` + strings.Join(residentialPropertyClasses, "', '") + `') THEN districtrate.residential_rate ELSE districtrate.non_residential_rate END/100 AS INT) / 100 AS total FROM parcel INNER JOIN districtrate USING(district) )`

		queryPrefix = strings.TrimSpace(queryPrefix)
		queryPrefix = strings.TrimRight(queryPrefix, ",")
		queryPrefix += "\n"
	}

	output := queryPrefix + query
	slog.InfoContext(context.TODO(), "IndexPage: Query", "query", output)
	return output
}

func (c *IndexPage) proposedQuery(query string) string {
	var queryPrefix string
	{
		residentialPropertyClasses := []string{
			"farmland",
			"residential",
		}

		var districtRateClause string
		{
			var parts []string
			for _, districtRate := range c.taxRates {
				if districtRate.SchoolDistrict == c.selectedSchoolDistrict {
					parts = append(parts, fmt.Sprintf("SELECT '%s' AS district, %f AS residential_rate, %f AS non_residential_rate", districtRate.SchoolDistrict, c.proposedResidentialRate, c.proposedNonResidentialRate))
				} else {
					parts = append(parts, fmt.Sprintf("SELECT '%s' AS district, %f AS residential_rate, %f AS non_residential_rate", districtRate.SchoolDistrict, districtRate.ResidentialRate, districtRate.NonResidentialRate))
				}
			}
			districtRateClause = strings.Join(parts, " UNION ")

			districtRateClause = "districtrate AS (" + strings.TrimSpace(districtRateClause) + ")"
		}

		var apartmentExpression string
		switch c.proposedApartmentClass {
		case "residential":
			apartmentExpression = "districtrate.residential_rate"
		case "non-residential":
			apartmentExpression = "districtrate.non_residential_rate"
		case "special":
			apartmentExpression = "districtrate.residential_rate * " + fmt.Sprintf("%0.4f", c.proposedSpecialApartmentMultiplier)
		}

		queryPrefix = "WITH\n" + districtRateClause + ",\n"
		queryPrefix += `parceltax AS ( SELECT parcel.parcelid AS parcelid, CAST(100 * parcel.school_taxable * CASE WHEN parcel.property_class IN ('` + strings.Join(residentialPropertyClasses, "', '") + `') THEN districtrate.residential_rate WHEN parcel.property_class = 'apartment' THEN ` + apartmentExpression + ` ELSE districtrate.non_residential_rate END/100 AS INT) / 100 AS total FROM parcel INNER JOIN districtrate USING(district) )`
		queryPrefix = strings.TrimSpace(queryPrefix)
		queryPrefix = strings.TrimRight(queryPrefix, ",")
		queryPrefix += "\n"
	}

	output := queryPrefix + query
	slog.InfoContext(context.TODO(), "IndexPage: Proposed query", "query", output)
	return output
}

type PropertyClassResult struct {
	PropertyClass string  `gorm:"column:property_class"`
	PropertyCount int64   `gorm:"column:property_count"`
	PropertyValue float64 `gorm:"column:property_value"`
	PropertyTax   float64 `gorm:"column:property_tax"`
	AverageValue  float64 `gorm:"column:average_value"`
	AverageTax    float64 `gorm:"column:average_tax"`
	MedianValue   float64 `gorm:"column:median_value"`
	MedianTax     float64 `gorm:"column:median_tax"`
}

type TaxRate struct {
	SchoolDistrict     string  `gorm:"column:district"`
	ResidentialRate    float64 `gorm:"column:residential_rate"`
	NonResidentialRate float64 `gorm:"column:non_residential_rate"`
}

func (TaxRate) TableName() string {
	return "taxrate"
}

type Parcel struct {
	ParcelID       string  `gorm:"column:parcelid;primaryKey"`
	SchoolDistrict string  `gorm:"column:district;index:district_and_property_class,priority:1"`
	PropertyClass  string  `gorm:"column:property_class;index:district_and_property_class,priority:2"`
	SchoolTaxable  float64 `gorm:"column:school_taxable"`
	CountyTaxable  float64 `gorm:"column:county_taxable"`
}

func (Parcel) TableName() string {
	return "parcel"
}

func (c *IndexPage) OnMount(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: OnMount")

	c.maximumRateMultiplier = 2.0
	c.proposedApartmentClass = "non-residential"
	c.proposedSpecialApartmentMultiplier = 1.2
	c.taxRates = []TaxRate{
		{
			SchoolDistrict:     "christina",
			ResidentialRate:    0.6150,
			NonResidentialRate: 1.2102,
		},
		{
			SchoolDistrict:     "brandywine",
			ResidentialRate:    0.5609,
			NonResidentialRate: 1.0382,
		},
		{
			SchoolDistrict:     "colonial",
			ResidentialRate:    0.4523,
			NonResidentialRate: 0.74294,
		},
		{
			SchoolDistrict:     "redclay",
			ResidentialRate:    0.5918,
			NonResidentialRate: 0.99237,
		},
		{
			SchoolDistrict:     "appoquinimink",
			ResidentialRate:    0.57692,
			NonResidentialRate: 1.15378,
		},
	}

	subFS, err := fs.Sub(dataset.EmbeddedFS, "embedded")
	if err != nil {
		slog.ErrorContext(ctx.Context, "IndexPage: Error creating sub FS", "err", err)
		return
	}

	file, err := subFS.Open("parcels.2026.christina.csv")
	if err != nil {
		slog.ErrorContext(ctx.Context, "IndexPage: Error opening file", "err", err)
		return
	}
	defer file.Close()

	csvReader := csv.NewReader(file)
	rows, err := csvReader.ReadAll()
	if err != nil {
		slog.ErrorContext(ctx.Context, "IndexPage: Error reading CSV", "err", err)
		return
	}

	header := rows[0]
	rows = rows[1:]

	headerToIndexMap := map[string]int{}
	for i, header := range header {
		headerToIndexMap[strings.ToLower(header)] = i
	}

	memdb.Create("my_shared_db", nil)

	db, err := database.New(ctx.Context, "sqlite3", "file:/my_shared_db?vfs=memdb&cache=shared&parseTime=true")
	if err != nil {
		slog.ErrorContext(ctx.Context, "IndexPage: Error creating database", "err", err)
		return
	}
	err = db.AutoMigrate(&Parcel{})
	if err != nil {
		slog.ErrorContext(ctx.Context, "IndexPage: Error migrating database", "err", err)
		return
	}

	schoolDistrictMap := map[string]bool{}

	var parcels []Parcel
	for _, row := range rows {
		parcel := Parcel{
			ParcelID:      row[headerToIndexMap["parcelid"]],
			PropertyClass: strings.ToLower(row[headerToIndexMap["prop class"]]),
		}
		if strings.Contains(strings.ToLower(row[headerToIndexMap["descript"]]), "christina") {
			parcel.SchoolDistrict = "christina"
		} else if strings.Contains(strings.ToLower(row[headerToIndexMap["descript"]]), "brandywine") {
			parcel.SchoolDistrict = "brandywine"
		} else if strings.Contains(strings.ToLower(row[headerToIndexMap["descript"]]), "colonial") {
			parcel.SchoolDistrict = "colonial"
		} else if strings.Contains(strings.ToLower(row[headerToIndexMap["descript"]]), "redclay") {
			parcel.SchoolDistrict = "redclay"
		} else if strings.Contains(strings.ToLower(row[headerToIndexMap["descript"]]), "appoquinimink") {
			parcel.SchoolDistrict = "appoquinimink"
		}

		v, err := strconv.ParseUint(row[headerToIndexMap["school taxable"]], 10, 64)
		if err != nil {
			slog.ErrorContext(ctx.Context, "IndexPage: Error parsing school taxable", "err", err)
			return
		}
		parcel.SchoolTaxable = float64(v)
		v, err = strconv.ParseUint(row[headerToIndexMap["county taxable"]], 10, 64)
		if err != nil {
			slog.ErrorContext(ctx.Context, "IndexPage: Error parsing county taxable", "err", err)
			return
		}
		parcel.CountyTaxable = float64(v)

		parcels = append(parcels, parcel)
		schoolDistrictMap[parcel.SchoolDistrict] = true
	}
	err = db.CreateInBatches(parcels, 1000).Error
	if err != nil {
		slog.ErrorContext(ctx.Context, "IndexPage: Error creating parcels", "err", err)
		return
	}

	c.db = db

	c.schoolDistricts = slices.Collect(maps.Keys(schoolDistrictMap))
	slices.Sort(c.schoolDistricts)
}

func (c *IndexPage) OnNav(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: OnNav")
}

func (c *IndexPage) Render() app.UI {
	return blazar.Page().
		Body(
			app.Div().
				Body(
					blazar.Select().
						Label("School District").
						AllowedValue(func() []blazar.SelectOption {
							options := []blazar.SelectOption{
								{
									Value:    "",
									Label:    "Select a school district",
									Disabled: true,
								},
								/*
									{
										Value: "all",
										Label: "All school districts",
									},
								*/
							}
							for _, district := range c.schoolDistricts {
								options = append(options, blazar.SelectOption{
									Value: district,
									Label: district,
								})
							}
							return options
						}()...).
						Bind(&c.selectedSchoolDistrict).
						On("change", func(ctx app.Context, e app.Event) {
							slog.InfoContext(ctx.Context, "IndexPage: School district changed", "selectedSchoolDistrict", c.selectedSchoolDistrict)

							ctx.Async(func() {
								c.updateForNewSchoolDistrict(ctx)
							})
						}),
				),
			app.If(len(c.propertyClassResults2025) > 0, func() app.UI {
				return app.Div().
					Body(
						blazar.Table[PropertyClassResult]().
							Title("2025").
							Rows(c.propertyClassResults2025).
							Columns([]blazar.TableColumn[PropertyClassResult]{
								{
									Name: "Property Class",
									Value: func(row PropertyClassResult) any {
										return row.PropertyClass
									},
								},
								{
									Name: "Property Count",
									Value: func(row PropertyClassResult) any {
										return printer.Sprintf("%d", row.PropertyCount)
									},
								},
								{
									Name: "Property Value",
									Value: func(row PropertyClassResult) any {
										return printer.Sprintf("$%.0f", row.PropertyValue)
									},
								},
								{
									Name: "Property Tax",
									Value: func(row PropertyClassResult) any {
										return printer.Sprintf("$%.0f", row.PropertyTax)
									},
								},
								{
									Name: "Average Value",
									Value: func(row PropertyClassResult) any {
										return printer.Sprintf("$%.0f", row.AverageValue)
									},
								},
								{
									Name: "Average Tax",
									Value: func(row PropertyClassResult) any {
										return printer.Sprintf("$%.0f", row.AverageTax)
									},
								},
								{
									Name: "Median Value",
									Value: func(row PropertyClassResult) any {
										return printer.Sprintf("$%.0f", row.MedianValue)
									},
								},
								{
									Name: "Median Tax",
									Value: func(row PropertyClassResult) any {
										return printer.Sprintf("$%.0f", row.MedianTax)
									},
								},
							}),
						app.FieldSet().
							Body(
								app.Legend().Text("Current"),
								blazar.Input[string]().
									Label("Total Tax").
									Disabled(true).
									Value(printer.Sprintf("$%.0f", c.totalTax)),
								blazar.Input[float64]().
									Label("Non-Residential Rate Multiplier").
									Disabled(true).
									Value(c.nonResidentialRateMultiplier).
									On("change", func(ctx app.Context, e app.Event) {
										slog.InfoContext(ctx.Context, "IndexPage: Non-Residential Rate Multiplier changed", "proposedNonResidentialRateMultiplier", c.proposedNonResidentialRateMultiplier)

										c.refigureProposal(ctx)
									}),
								blazar.Input[float64]().
									Label("Residential Rate").
									Disabled(true).
									Value(c.residentialRate).
									On("change", func(ctx app.Context, e app.Event) {
										slog.InfoContext(ctx.Context, "IndexPage: Residential Rate changed", "proposedResidentialRate", c.proposedResidentialRate)

										c.refigureProposal(ctx)
									}),
								blazar.Input[float64]().
									Label("Non-Residential Rate").
									Disabled(true).
									Value(c.nonResidentialRate).
									On("change", func(ctx app.Context, e app.Event) {
										slog.InfoContext(ctx.Context, "IndexPage: Non-Residential Rate changed", "proposedNonResidentialRate", c.proposedNonResidentialRate)

										c.refigureProposal(ctx)
									}),
							),
						app.FieldSet().
							Body(
								app.Legend().Text("Proposed"),
								blazar.Input[float64]().
									Label("Maximum Rate Multiplier").
									Disabled(c.calculating).
									Bind(&c.maximumRateMultiplier).
									On("change", func(ctx app.Context, e app.Event) {
										slog.InfoContext(ctx.Context, "IndexPage: Maximum Rate Multiplier changed", "maximumRateMultiplier", c.maximumRateMultiplier)

										c.refigureProposal(ctx)
									}),
								blazar.Input[float64]().
									Label("Non-Residential Rate Multiplier").
									Disabled(c.calculating).
									Bind(&c.proposedNonResidentialRateMultiplier).
									On("change", func(ctx app.Context, e app.Event) {
										slog.InfoContext(ctx.Context, "IndexPage: Non-Residential Rate Multiplier changed", "proposedNonResidentialRateMultiplier", c.proposedNonResidentialRateMultiplier)

										c.refigureProposal(ctx)
									}),
								blazar.Input[float64]().
									Label("Residential Rate").
									Disabled(c.calculating).
									Bind(&c.proposedResidentialRate).
									On("change", func(ctx app.Context, e app.Event) {
										slog.InfoContext(ctx.Context, "IndexPage: Residential Rate changed", "proposedResidentialRate", c.proposedResidentialRate)

										c.refigureProposal(ctx)
									}),
								blazar.Input[float64]().
									Label("Non-Residential Rate").
									Disabled(c.calculating).
									Bind(&c.proposedNonResidentialRate).
									On("change", func(ctx app.Context, e app.Event) {
										slog.InfoContext(ctx.Context, "IndexPage: Non-Residential Rate changed", "proposedNonResidentialRate", c.proposedNonResidentialRate)

										c.refigureProposal(ctx)
									}),
								blazar.Select().
									Label("Apartments Are Classifed As").
									//Disabled(c.calculating).
									Bind(&c.proposedApartmentClass).
									AllowedValue(
										blazar.SelectOption{},
										blazar.SelectOption{
											Label: "Residential",
											Value: "residential",
										},
										blazar.SelectOption{
											Label: "Non-Residential",
											Value: "non-residential",
										},
										blazar.SelectOption{
											Label: "Special New Thing",
											Value: "special",
										},
									).
									On("change", func(ctx app.Context, e app.Event) {
										slog.InfoContext(ctx.Context, "IndexPage: Apartments Are Classified As changed", "proposedApartmentClass", c.proposedApartmentClass)

										c.refigureProposal(ctx)
									}),
								blazar.Input[float64]().
									Label("Special Apartment Multiplier").
									Disabled(c.calculating).
									Bind(&c.proposedSpecialApartmentMultiplier).
									On("change", func(ctx app.Context, e app.Event) {
										slog.InfoContext(ctx.Context, "IndexPage: Special Apartment Multiplier changed", "proposedSpecialApartmentRate", c.proposedSpecialApartmentMultiplier)

										c.refigureProposal(ctx)
									}),
							),
						blazar.Button().
							Label("Calculate").
							Disabled(c.calculating).
							On("click", func(ctx app.Context, e app.Event) {
								slog.InfoContext(ctx.Context, "IndexPage: Calculate button clicked")

								c.calculateProposedPropertyClassResults(ctx)
							}),
					)
			}),
			app.If(len(c.proposedPropertyClassResults) > 0, func() app.UI {
				return app.Div().
					Body(
						blazar.Input[string]().
							Label("Proposed Total Tax").
							Disabled(true).
							Value(printer.Sprintf("$%.0f", c.proposedTotalTax)),
						blazar.Table[PropertyClassResult]().
							Rows(c.proposedPropertyClassResults).
							Columns([]blazar.TableColumn[PropertyClassResult]{
								{
									Name: "Property Class",
									Value: func(row PropertyClassResult) any {
										return row.PropertyClass
									},
								},
								{
									Name: "Property Count",
									Value: func(row PropertyClassResult) any {
										return printer.Sprintf("%d", row.PropertyCount)
									},
								},
								{
									Name: "Property Value",
									Value: func(row PropertyClassResult) any {
										return printer.Sprintf("$%.0f", row.PropertyValue)
									},
								},
								{
									Name: "Property Tax",
									Value: func(row PropertyClassResult) any {
										return printer.Sprintf("$%.0f", row.PropertyTax)
									},
								},
								{
									Name: "Average Value",
									Value: func(row PropertyClassResult) any {
										return printer.Sprintf("$%.0f", row.AverageValue)
									},
								},
								{
									Name: "Average Tax",
									Value: func(row PropertyClassResult) any {
										return printer.Sprintf("$%.0f", row.AverageTax)
									},
								},
								{
									Name: "Median Value",
									Value: func(row PropertyClassResult) any {
										return printer.Sprintf("$%.0f", row.MedianValue)
									},
								},
								{
									Name: "Median Tax",
									Value: func(row PropertyClassResult) any {
										return printer.Sprintf("$%.0f", row.MedianTax)
									},
								},
							}),
					)
			}),
		)
}

func (c *IndexPage) updateForNewSchoolDistrict(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: updateForNewSchoolDistrict", "selectedSchoolDistrict", c.selectedSchoolDistrict)

	c.loading = true

	ctx.Update()

	if c.selectedSchoolDistrict == "" {
		return
	}

	ctx.Async(func() {
		{
			var districtRate TaxRate
			for _, testRate := range c.taxRates {
				if testRate.SchoolDistrict == c.selectedSchoolDistrict {
					districtRate = testRate
					break
				}
			}
			if districtRate.SchoolDistrict == "" {
				slog.ErrorContext(ctx.Context, "IndexPage: updateForNewSchoolDistrict: District rate not found", "selectedSchoolDistrict", c.selectedSchoolDistrict)
				return
			}

			c.nonResidentialRateMultiplier = districtRate.NonResidentialRate / districtRate.ResidentialRate
			c.residentialRate = districtRate.ResidentialRate
			c.nonResidentialRate = districtRate.NonResidentialRate

			c.proposedNonResidentialRateMultiplier = c.nonResidentialRateMultiplier
			c.proposedResidentialRate = c.residentialRate
			c.proposedNonResidentialRate = c.nonResidentialRate
		}

		{
			query := c.query(`
SELECT
parcel.property_class,
COUNT(*) AS property_count,
SUM(parcel.school_taxable) AS property_value,
SUM(parceltax.total) AS property_tax,
AVG(parcel.school_taxable) AS average_value,
AVG(parceltax.total) AS average_tax,
MEDIAN(parcel.school_taxable) AS median_value,
MEDIAN(parceltax.total) AS median_tax
FROM parcel
INNER JOIN parceltax USING(parcelid)
WHERE 1
AND parcel.district = ?
AND parcel.property_class NOT LIKE '%exempt%'
GROUP BY parcel.property_class
`)
			var propertyClassResults []PropertyClassResult
			err := c.db.Raw(query, c.selectedSchoolDistrict).
				Find(&propertyClassResults).
				Error
			if err != nil {
				slog.ErrorContext(ctx.Context, "IndexPage: Error executing query", "err", err)
				return
			}

			c.propertyClassResults2025 = propertyClassResults
		}

		c.totalTax = 0
		for _, propertyClassResult := range c.propertyClassResults2025 {
			c.totalTax += propertyClassResult.PropertyTax
		}

		c.loading = false

		ctx.Update()
	})
}

func (c *IndexPage) refigureProposal(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: apartments are residential", "apartmentsAreResidential", c.proposedApartmentClass)

	slog.InfoContext(ctx.Context, "IndexPage: rates", "residentialRate", c.residentialRate, "nonResidentialRateMultiplier", c.nonResidentialRateMultiplier)

	if c.proposedNonResidentialRateMultiplier > c.maximumRateMultiplier {
		c.proposedNonResidentialRateMultiplier = c.maximumRateMultiplier
	}

	residentialTotalValue := 0.0
	apartmentTotalValue := 0.0
	nonResidentialTotalValue := 0.0
	for _, propertyClassResult := range c.propertyClassResults2025 {
		if slices.Contains([]string{"farmland", "residential"}, propertyClassResult.PropertyClass) {
			residentialTotalValue += propertyClassResult.PropertyValue
		} else if propertyClassResult.PropertyClass == "apartment" {
			apartmentTotalValue += propertyClassResult.PropertyValue
		} else {
			nonResidentialTotalValue += propertyClassResult.PropertyValue
		}
	}
	totalValue := residentialTotalValue + nonResidentialTotalValue + apartmentTotalValue
	slog.InfoContext(ctx.Context, "IndexPage: original values", "residentialTotalValue", printer.Sprintf("$%.0f", residentialTotalValue), "nonResidentialTotalValue", printer.Sprintf("$%.0f", nonResidentialTotalValue), "apartmentTotalValue", printer.Sprintf("$%.0f", apartmentTotalValue))
	slog.InfoContext(ctx.Context, "IndexPage: total value", "totalValue", printer.Sprintf("$%.0f", totalValue))

	currentApartmentMultiplier := c.nonResidentialRateMultiplier
	var proposedApartmentMultiplier float64
	switch c.proposedApartmentClass {
	case "residential":
		proposedApartmentMultiplier = 1.0
	case "non-residential":
		proposedApartmentMultiplier = c.proposedNonResidentialRateMultiplier
	case "special":
		proposedApartmentMultiplier = c.proposedSpecialApartmentMultiplier
	}

	testRevenue := c.residentialRate/100.0*residentialTotalValue + c.nonResidentialRateMultiplier*c.residentialRate/100.0*nonResidentialTotalValue + currentApartmentMultiplier*c.residentialRate/100.0*apartmentTotalValue
	slog.InfoContext(ctx.Context, "IndexPage: revenue", "oldRevenue", printer.Sprintf("$%.0f", c.totalTax), "original testRevenue", printer.Sprintf("$%.0f", testRevenue))

	newResidentialTotalValue := residentialTotalValue
	newNonResidentialTotalValue := nonResidentialTotalValue
	newApartmentTotalValue := apartmentTotalValue

	slog.InfoContext(ctx.Context, "IndexPage: new values", "newResidentialTotalValue", printer.Sprintf("$%.0f", newResidentialTotalValue), "newNonResidentialTotalValue", printer.Sprintf("$%.0f", newNonResidentialTotalValue))

	//
	// Revenue = Residential Value * Residential Rate / 100 + Non-Residential Value * Non-Residential Rate / 100
	// Revenue = Residential Value * Residential Rate / 100 + Non-Residential Value * Multiplier * Residential Rate / 100
	// Revenue = Residential Rate / 100 * ( Residential Value + Non-Residential Value * Multiplier )
	// Revenue * 100 = Residential Rate * ( Residential Value + Non-Residential Value * Multiplier )
	// Revenue * 100 / Residential Rate = Residential Value + Non-Residential Value * Multiplier
	// Revenue * 100 / Residential Rate - Residential Value = Non-Residential Value * Multiplier
	// ( Revenue * 100 / Residential Rate - Residential Value ) / Non-Residential Value = Multiplier
	//
	//
	// Revenue = Residential Rate / 100 * ( Residential Value + Non-Residential Value * Multiplier )
	// Revenue * 100 = Residential Rate * ( Residential Value + Non-Residential Value * Multiplier )
	// Revenue * 100 / ( Residential Value + Non-Residential Value * Multiplier ) = Residential Rate
	//
	// Revenue = Residential Value * Residential Rate / 100 + Non-Residential Value * Multiplier * Residential Rate / 100 + Apartment Value * Apartment Multiplier * Residential Rate / 100
	// Revenue = Residential Rate / 100 * ( Residential Value + Non-Residential Value * Multiplier + Apartment Value * Apartment Multiplier )
	// Revenue * 100 / Residential Rate = Residential Value + Non-Residential Value * Multiplier + Apartment Value * Apartment Multiplier
	// ( Revenue * 100 / Residential Rate ) - Residential Value - Apartment Value * Apartment Multiplier = Non-Residential Value * Multiplier
	// ( ( Revenue * 100 / Residential Rate ) - Residential Value - Apartment Value * Apartment Multiplier ) / Non-Residential Value = Multiplier

	newMultiplier := (c.totalTax*100.0/c.proposedResidentialRate - newResidentialTotalValue - newApartmentTotalValue*proposedApartmentMultiplier) / newNonResidentialTotalValue
	slog.InfoContext(ctx.Context, "IndexPage: new multiplier", "newMultiplier", fmt.Sprintf("%.4f", newMultiplier))
	slog.InfoContext(ctx.Context, "IndexPage: new rates", "residentialRate", fmt.Sprintf("%.4f", c.proposedResidentialRate), "nonResidentialRate", fmt.Sprintf("%.4f", c.proposedResidentialRate*newMultiplier))

	if newMultiplier <= c.maximumRateMultiplier {
		newRevenue := c.proposedResidentialRate/100.0*newResidentialTotalValue + newMultiplier*c.proposedResidentialRate/100.0*newNonResidentialTotalValue
		slog.InfoContext(ctx.Context, "IndexPage: revenue", "oldRevenue", printer.Sprintf("$%.0f", c.totalTax), "newRevenue", printer.Sprintf("$%.0f", newRevenue))

		c.proposedNonResidentialRateMultiplier = newMultiplier
		c.proposedResidentialRate = c.residentialRate
	} else {
		c.proposedNonResidentialRateMultiplier = c.maximumRateMultiplier
		c.proposedResidentialRate = c.totalTax * 100.0 / (newResidentialTotalValue + newNonResidentialTotalValue*c.maximumRateMultiplier + newApartmentTotalValue*proposedApartmentMultiplier)
	}
	c.proposedNonResidentialRate = c.proposedResidentialRate * c.proposedNonResidentialRateMultiplier

	testRevenue = c.proposedResidentialRate/100.0*residentialTotalValue + c.proposedNonResidentialRateMultiplier*c.proposedResidentialRate/100.0*nonResidentialTotalValue + proposedApartmentMultiplier*c.proposedResidentialRate/100.0*apartmentTotalValue
	slog.InfoContext(ctx.Context, "IndexPage: revenue", "oldRevenue", printer.Sprintf("$%.0f", c.totalTax), "final testRevenue", printer.Sprintf("$%.0f", testRevenue))

	ctx.Update()
}

func (c *IndexPage) calculateProposedPropertyClassResults(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: calculateProposedPropertyClassResults")

	c.calculating = true
	c.proposedPropertyClassResults = []PropertyClassResult{}

	ctx.Update()

	if c.selectedSchoolDistrict == "" {
		return
	}

	ctx.Async(func() {
		iterations := 0
		for {
			iterations++
			if iterations > 100 {
				slog.ErrorContext(ctx.Context, "IndexPage: calculateProposedPropertyClassResults: Too many iterations", "iterations", iterations)
				return
			}

			{
				query := c.proposedQuery(`
SELECT
parcel.property_class,
COUNT(*) AS property_count,
SUM(parcel.school_taxable) AS property_value,
SUM(parceltax.total) AS property_tax,
AVG(parcel.school_taxable) AS average_value,
AVG(parceltax.total) AS average_tax,
MEDIAN(parcel.school_taxable) AS median_value,
MEDIAN(parceltax.total) AS median_tax
FROM parcel
INNER JOIN parceltax USING(parcelid)
WHERE 1
AND parcel.district = ?
AND parcel.property_class NOT LIKE '%exempt%'
GROUP BY parcel.property_class
`)
				var propertyClassResults []PropertyClassResult
				err := c.db.Raw(query, c.selectedSchoolDistrict).
					Find(&propertyClassResults).
					Error
				if err != nil {
					slog.ErrorContext(ctx.Context, "IndexPage: calculateProposedPropertyClassResults: Error executing query", "err", err)
					return
				}

				c.proposedPropertyClassResults = propertyClassResults
			}

			c.proposedTotalTax = 0
			for _, propertyClassResult := range c.proposedPropertyClassResults {
				c.proposedTotalTax += propertyClassResult.PropertyTax
			}

			slog.InfoContext(ctx.Context, "IndexPage: calculateProposedPropertyClassResults: proposed total tax", "proposedTotalTax", printer.Sprintf("$%.0f", c.proposedTotalTax), "totalTax", printer.Sprintf("$%.0f", c.totalTax))
			if c.proposedTotalTax >= c.totalTax*0.995 && c.proposedTotalTax <= c.totalTax*1.005 {
				break
			}

			if c.proposedTotalTax < c.totalTax {
				slog.InfoContext(ctx.Context, "IndexPage: calculateProposedPropertyClassResults: proposed total tax is less than total tax", "proposedTotalTax", printer.Sprintf("$%.0f", c.proposedTotalTax), "totalTax", printer.Sprintf("$%.0f", c.totalTax))
				c.proposedResidentialRate *= 1.005
			} else {
				slog.InfoContext(ctx.Context, "IndexPage: calculateProposedPropertyClassResults: proposed total tax is greater than total tax", "proposedTotalTax", printer.Sprintf("$%.0f", c.proposedTotalTax), "totalTax", printer.Sprintf("$%.0f", c.totalTax))
				c.proposedResidentialRate *= 0.995
			}
			c.proposedNonResidentialRate = c.proposedResidentialRate * c.proposedNonResidentialRateMultiplier
			slog.InfoContext(ctx.Context, "IndexPage: calculateProposedPropertyClassResults: setting new proposed residential rate", "proposedResidentialRate", fmt.Sprintf("%.4f", c.proposedResidentialRate))
		}

		c.calculating = false

		ctx.Update()
	})
}
