package demo

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"slices"
	"strings"

	"github.com/go-app-blazar/blazar/blazar"
	"github.com/maxence-charriere/go-app/v11/pkg/app"
	"github.com/ncruces/go-sqlite3/util/ioutil"
	"github.com/ncruces/go-sqlite3/vfs/readervfs"
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
	districtRates   []DistrictRate
	countyRates     []CountyRate

	selectedSchoolDistrict string

	maximumRateMultiplier float64

	residentialRate              float64
	nonResidentialRate           float64
	nonResidentialRateMultiplier float64

	propertyClassResults []PropertyClassResult
	totalTax             float64

	proposedResidentialRate              float64
	proposedNonResidentialRate           float64
	proposedNonResidentialRateMultiplier float64
	proposedApartmentsAreResidential     bool

	proposedPropertyClassResults []PropertyClassResult
	proposedTotalTax             float64
}

var printer *message.Printer = message.NewPrinter(language.English)

/*
		return strings.TrimSpace(`
	WITH
	countyrates AS (SELECT 0.21 AS votech2024, 0.0431 AS votech2025),
	districtrates AS (SELECT 'christina' AS district, 3.224 AS rate2024, 0.6150 AS rate2025, 1.2102 AS nrate2025 UNION SELECT 'brandywine' AS district, 2.7685 AS rate2024, 0.5609 AS rate2025, 1.0382 AS nrate2025 UNION SELECT 'colonial' AS district, 2.296 AS rate2024, 0.4523 AS rate2025, 0.74294 AS nrate2025 UNION SELECT 'redclay' AS district, 2.658 AS rate2024, 0.5918 AS rate2025, 0.99237 AS nrate2025 UNION SELECT 'appoquinimink' AS district, 3.1454 AS rate2024, 0.57692 AS rate2025, 1.15378 AS nrate2025),
	credit2025 AS ( SELECT parceltax.parcelid AS parcelid, CAST(100 * ( parcelassessment.school_taxable * (districtrates.rate2025 + countyrates.votech2025)/100 - ( school_amount_paid + school_principal_due ) ) AS INT) / 100 AS credit FROM parcel INNER JOIN parceltax USING(parcelid) INNER JOIN parcelassessment ON parceltax.parcelid = parcelassessment.parcelid AND parcelassessment.type = 'current' INNER JOIN districtrates USING(district) CROSS JOIN countyrates WHERE parceltax.year = '2025A' ),
	parceltax2024 AS ( SELECT parceltax.parcelid AS parcelid, ( school_amount_paid + school_principal_due ) * (districtrates.rate2024 / (districtrates.rate2024+countyrates.votech2024)) + credit2025.credit AS total FROM parcel INNER JOIN parceltax USING(parcelid) INNER JOIN credit2025 USING(parcelid) INNER JOIN districtrates USING(district) CROSS JOIN countyrates WHERE parceltax.year = '2024'),
	parcelvalue2024 AS ( SELECT parcel.parcelid AS parcelid, parceltax2024.total / (districtrates.rate2024/100) AS total FROM parcel INNER JOIN parceltax2024 USING(parcelid) INNER JOIN districtrates USING (district) ),
	parceltax2025 AS ( SELECT parcelassessment.parcelid AS parcelid, CAST(100 * parcelassessment.school_taxable * CASE WHEN parcel.property_class IN ('`+strings.Join(residentialPropertyClasses, "', '")+`') THEN districtrates.rate2025 ELSE districtrates.nrate2025 END/100 AS INT) / 100 + credit2025.credit AS total FROM parcel INNER JOIN parcelassessment USING(parcelid) INNER JOIN credit2025 USING(parcelid) INNER JOIN districtrates USING(district) CROSS JOIN countyrates WHERE parcelassessment.type = 'current' ),
	parcelvalue2025 AS ( SELECT parcelid, county_taxable, school_taxable, school_taxable AS total FROM parcelassessment INNER JOIN parcel USING(parcelid) WHERE type = 'current' )
	`) + "\n"
*/

func (c *IndexPage) query(query string) string {
	var queryPrefix string
	{
		residentialPropertyClasses := []string{
			"FARMLAND",
			"RESIDENTIAL",
		}

		queryPrefix = strings.TrimSpace(`
WITH
countyrates AS (SELECT 0.21 AS votech2024, 0.0431 AS votech2025),
districtrates AS (SELECT 'christina' AS district, 3.224 AS rate2024, 0.6150 AS rate2025, 1.2102 AS nrate2025 UNION SELECT 'brandywine' AS district, 2.7685 AS rate2024, 0.5609 AS rate2025, 1.0382 AS nrate2025 UNION SELECT 'colonial' AS district, 2.296 AS rate2024, 0.4523 AS rate2025, 0.74294 AS nrate2025 UNION SELECT 'redclay' AS district, 2.658 AS rate2024, 0.5918 AS rate2025, 0.99237 AS nrate2025 UNION SELECT 'appoquinimink' AS district, 3.1454 AS rate2024, 0.57692 AS rate2025, 1.15378 AS nrate2025),
credit2025 AS ( SELECT parceltax.parcelid AS parcelid, CAST(100 * ( parcelassessment.school_taxable * (districtrates.rate2025 + countyrates.votech2025)/100 - ( school_amount_paid + school_principal_due ) ) AS INT) / 100 AS credit FROM parcel INNER JOIN parceltax USING(parcelid) INNER JOIN parcelassessment ON parceltax.parcelid = parcelassessment.parcelid AND parcelassessment.type = 'current' INNER JOIN districtrates USING(district) CROSS JOIN countyrates WHERE parceltax.year = '2025A' ),
`)
		if strings.Contains(query, "parcelvalue2025") {
			queryPrefix += `
parceltax2025 AS ( SELECT parcelassessment.parcelid AS parcelid, CAST(100 * parcelassessment.school_taxable * CASE WHEN parcel.property_class IN ('` + strings.Join(residentialPropertyClasses, "', '") + `') THEN districtrates.rate2025 ELSE districtrates.nrate2025 END/100 AS INT) / 100 + credit2025.credit AS total FROM parcel INNER JOIN parcelassessment USING(parcelid) INNER JOIN credit2025 USING(parcelid) INNER JOIN districtrates USING(district) CROSS JOIN countyrates WHERE parcelassessment.type = 'current' ),
parcelvalue2025 AS ( SELECT parcelid, county_taxable, school_taxable, school_taxable AS total FROM parcelassessment INNER JOIN parcel USING(parcelid) WHERE type = 'current' ),
`
		}
		if strings.Contains(query, "parcelvalue2024") {
			queryPrefix += strings.TrimSpace(`
parceltax2024 AS ( SELECT parceltax.parcelid AS parcelid, ( school_amount_paid + school_principal_due ) * (districtrates.rate2024 / (districtrates.rate2024+countyrates.votech2024)) + credit2025.credit AS total FROM parcel INNER JOIN parceltax USING(parcelid) INNER JOIN credit2025 USING(parcelid) INNER JOIN districtrates USING(district) CROSS JOIN countyrates WHERE parceltax.year = '2024'),
parcelvalue2024 AS ( SELECT parcel.parcelid AS parcelid, parceltax2024.total / (districtrates.rate2024/100) AS total FROM parcel INNER JOIN parceltax2024 USING(parcelid) INNER JOIN districtrates USING (district) ),
`)
		}

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
			"FARMLAND",
			"RESIDENTIAL",
		}
		if c.proposedApartmentsAreResidential {
			residentialPropertyClasses = append(residentialPropertyClasses, "APARTMENT")
		}

		queryPrefix = strings.TrimSpace(`
WITH
countyrates AS (SELECT 0.21 AS votech2024, 0.0431 AS votech2025),
districtrates AS (SELECT '` + c.selectedSchoolDistrict + `' AS district, ` + fmt.Sprintf("%f", c.proposedResidentialRate) + ` AS rate2025, ` + fmt.Sprintf("%f", c.proposedNonResidentialRate) + ` AS nrate2025),
credit2025 AS ( SELECT parceltax.parcelid AS parcelid, CAST(100 * ( parcelassessment.school_taxable * (districtrates.rate2025 + countyrates.votech2025)/100 - ( school_amount_paid + school_principal_due ) ) AS INT) / 100 AS credit FROM parcel INNER JOIN parceltax USING(parcelid) INNER JOIN parcelassessment ON parceltax.parcelid = parcelassessment.parcelid AND parcelassessment.type = 'current' INNER JOIN districtrates USING(district) CROSS JOIN countyrates WHERE parceltax.year = '2025A' ),
`)
		if strings.Contains(query, "parcelvalue2025") {
			queryPrefix += `
parceltax2025 AS ( SELECT parcelassessment.parcelid AS parcelid, CAST(100 * parcelassessment.school_taxable * CASE WHEN parcel.property_class IN ('` + strings.Join(residentialPropertyClasses, "', '") + `') THEN districtrates.rate2025 ELSE districtrates.nrate2025 END/100 AS INT) / 100 + credit2025.credit AS total FROM parcel INNER JOIN parcelassessment USING(parcelid) INNER JOIN credit2025 USING(parcelid) INNER JOIN districtrates USING(district) CROSS JOIN countyrates WHERE parcelassessment.type = 'current' ),
parcelvalue2025 AS ( SELECT parcelid, county_taxable, school_taxable, school_taxable AS total FROM parcelassessment INNER JOIN parcel USING(parcelid) WHERE type = 'current' ),
`
		}

		queryPrefix = strings.TrimSpace(queryPrefix)
		queryPrefix = strings.TrimRight(queryPrefix, ",")
		queryPrefix += "\n"
	}

	output := queryPrefix + query
	slog.InfoContext(context.TODO(), "IndexPage: Proposed query", "query", output)
	return output
}

type DistrictRate struct {
	District               string  `gorm:"column:district"`
	Rate2024               float64 `gorm:"column:rate2024"`
	ResidentialRate2025    float64 `gorm:"column:rate2025"`
	NonResidentialRate2025 float64 `gorm:"column:nrate2025"`
}

type CountyRate struct {
	Votech2024 float64 `gorm:"column:votech2024"`
	Votech2025 float64 `gorm:"column:votech2025"`
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

func (c *IndexPage) OnMount(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: OnMount")

	c.maximumRateMultiplier = 2.0

	subFS, err := fs.Sub(dataset.EmbeddedFS, "embedded")
	if err != nil {
		slog.ErrorContext(ctx.Context, "IndexPage: Error creating sub FS", "err", err)
		return
	}

	file, err := subFS.Open("database.county.sqlite.gz")
	if err != nil {
		slog.ErrorContext(ctx.Context, "IndexPage: Error opening file", "err", err)
		return
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		slog.ErrorContext(ctx.Context, "IndexPage: Error creating gzip reader", "err", err)
		return
	}

	contents, err := io.ReadAll(gzipReader)
	if err != nil {
		slog.ErrorContext(ctx.Context, "IndexPage: Error reading file", "err", err)
		return
	}
	readervfs.Create("database.county.sqlite", ioutil.NewSizeReaderAt(bytes.NewReader(contents)))

	db, err := database.New(ctx.Context, "sqlite3", "file:database.county.sqlite?vfs=reader&cache=shared&parseTime=true")
	if err != nil {
		slog.ErrorContext(ctx.Context, "IndexPage: Error creating database", "err", err)
		return
	}

	err = db.Exec("PRAGMA temp_store = memory;").Error

	c.db = db

	var districtRates []DistrictRate
	err = c.db.Raw(c.query("SELECT district, rate2024, rate2025, nrate2025 FROM districtrates")).
		Scan(&districtRates).
		Error
	if err != nil {
		slog.ErrorContext(ctx.Context, "IndexPage: Error executing query", "err", err)
		return
	}
	c.districtRates = districtRates

	var countyRates []CountyRate
	err = c.db.Raw(c.query("SELECT votech2024, votech2025 FROM countyrates")).
		Scan(&countyRates).
		Error
	if err != nil {
		slog.ErrorContext(ctx.Context, "IndexPage: Error executing query", "err", err)
		return
	}
	c.countyRates = countyRates

	c.schoolDistricts = []string{}
	for _, districtRate := range c.districtRates {
		c.schoolDistricts = append(c.schoolDistricts, districtRate.District)
	}
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
			app.If(len(c.propertyClassResults) > 0, func() app.UI {
				return app.Div().
					Body(
						blazar.Table[PropertyClassResult]().
							Rows(c.propertyClassResults).
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

										c.updateNonResidentialRateMultiplier(ctx)
									}),
								blazar.Input[float64]().
									Label("Residential Rate").
									Disabled(true).
									Value(c.residentialRate).
									On("change", func(ctx app.Context, e app.Event) {
										slog.InfoContext(ctx.Context, "IndexPage: Residential Rate changed", "proposedResidentialRate", c.proposedResidentialRate)

										c.updateResidentialRate(ctx)
									}),
								blazar.Input[float64]().
									Label("Non-Residential Rate").
									Disabled(true).
									Value(c.nonResidentialRate).
									On("change", func(ctx app.Context, e app.Event) {
										slog.InfoContext(ctx.Context, "IndexPage: Non-Residential Rate changed", "proposedNonResidentialRate", c.proposedNonResidentialRate)

										c.updateNonResidentialRate(ctx)
									}),
							),
						app.FieldSet().
							Body(
								app.Legend().Text("Proposed"),
								blazar.Input[float64]().
									Label("Maximum Rate Multiplier").
									Bind(&c.maximumRateMultiplier).
									On("change", func(ctx app.Context, e app.Event) {
										slog.InfoContext(ctx.Context, "IndexPage: Maximum Rate Multiplier changed", "maximumRateMultiplier", c.maximumRateMultiplier)

										c.updateMaximumRateMultiplier(ctx)
									}),
								blazar.Input[float64]().
									Label("Non-Residential Rate Multiplier").
									Bind(&c.proposedNonResidentialRateMultiplier).
									On("change", func(ctx app.Context, e app.Event) {
										slog.InfoContext(ctx.Context, "IndexPage: Non-Residential Rate Multiplier changed", "proposedNonResidentialRateMultiplier", c.proposedNonResidentialRateMultiplier)

										c.updateNonResidentialRateMultiplier(ctx)
									}),
								blazar.Input[float64]().
									Label("Residential Rate").
									Bind(&c.proposedResidentialRate).
									On("change", func(ctx app.Context, e app.Event) {
										slog.InfoContext(ctx.Context, "IndexPage: Residential Rate changed", "proposedResidentialRate", c.proposedResidentialRate)

										c.updateResidentialRate(ctx)
									}),
								blazar.Input[float64]().
									Label("Non-Residential Rate").
									Bind(&c.proposedNonResidentialRate).
									On("change", func(ctx app.Context, e app.Event) {
										slog.InfoContext(ctx.Context, "IndexPage: Non-Residential Rate changed", "proposedNonResidentialRate", c.proposedNonResidentialRate)

										c.updateNonResidentialRate(ctx)
									}),
								blazar.Input[bool]().
									Label("Apartments are Residential").
									Bind(&c.proposedApartmentsAreResidential).
									On("change", func(ctx app.Context, e app.Event) {
										slog.InfoContext(ctx.Context, "IndexPage: Apartments are Residential changed", "proposedApartmentsAreResidential", c.proposedApartmentsAreResidential)

										c.updateApartmentsAreResidential(ctx)
									}),
							),
						blazar.Button().
							Label("Calculate").
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
							}),
					)
			}),
		)
}

func (c *IndexPage) updateForNewSchoolDistrict(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: updateForNewSchoolDistrict", "selectedSchoolDistrict", c.selectedSchoolDistrict)

	if c.selectedSchoolDistrict == "" {
		return
	}

	{
		query := c.query("SELECT * FROM districtrates WHERE district = ?")
		var districtRate DistrictRate
		err := c.db.Raw(query, c.selectedSchoolDistrict).
			Scan(&districtRate).
			Error
		if err != nil {
			slog.ErrorContext(ctx.Context, "Error executing query", "err", err)
			return
		}

		c.nonResidentialRateMultiplier = districtRate.NonResidentialRate2025 / districtRate.ResidentialRate2025
		c.residentialRate = districtRate.ResidentialRate2025
		c.nonResidentialRate = districtRate.NonResidentialRate2025

		c.proposedNonResidentialRateMultiplier = c.nonResidentialRateMultiplier
		c.proposedResidentialRate = c.residentialRate
		c.proposedNonResidentialRate = c.nonResidentialRate
	}

	{
		query := c.query(`
SELECT
parcel.property_class,
COUNT(*) AS property_count,
SUM(parcelvalue2025.school_taxable) AS property_value,
SUM(parceltax2025.total) AS property_tax,
AVG(parcelvalue2025.school_taxable) AS average_value,
AVG(parceltax2025.total) AS average_tax
FROM parcel
INNER JOIN parceltax2025 USING(parcelid)
INNER JOIN parcelvalue2025 USING(parcelid)
WHERE 1
AND parcel.district = ?
AND parcel.property_class NOT LIKE '%exempt%'
GROUP BY parcel.property_class
`)
		var propertyClassResults []PropertyClassResult
		err := c.db.Raw(query, c.selectedSchoolDistrict).
			Scan(&propertyClassResults).
			Error
		if err != nil {
			slog.ErrorContext(ctx.Context, "IndexPage: Error executing query", "err", err)
			return
		}

		c.propertyClassResults = propertyClassResults
	}

	c.totalTax = 0
	for _, propertyClassResult := range c.propertyClassResults {
		c.totalTax += propertyClassResult.PropertyTax
	}

	ctx.Update()
}

func (c *IndexPage) updateMaximumRateMultiplier(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: updateMaximumRateMultiplier")

	c.refigureProposal(ctx)
}

func (c *IndexPage) updateNonResidentialRateMultiplier(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: updateNonResidentialRateMultiplier")

	c.refigureProposal(ctx)
}

func (c *IndexPage) updateResidentialRate(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: updateResidentialRate")

	c.refigureProposal(ctx)
}

func (c *IndexPage) updateNonResidentialRate(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: updateNonResidentialRate")

	c.refigureProposal(ctx)
}

func (c *IndexPage) updateApartmentsAreResidential(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: updateApartmentsAreResidential")

	c.refigureProposal(ctx)
}

func (c *IndexPage) refigureProposal(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: apartments are residential", "apartmentsAreResidential", c.proposedApartmentsAreResidential)

	slog.InfoContext(ctx.Context, "IndexPage: rates", "residentialRate", c.residentialRate, "nonResidentialRateMultiplier", c.nonResidentialRateMultiplier)

	apartmentTotalValue := 0.0
	//apartmentTotalTax := 0.0
	for _, propertyClassResult := range c.propertyClassResults {
		if propertyClassResult.PropertyClass == "APARTMENT" {
			apartmentTotalValue += propertyClassResult.PropertyTax * 100.0 / c.nonResidentialRate
			//apartmentTotalTax += propertyClassResult.PropertyTax
		}
	}

	residentialTotalValue := 0.0
	nonResidentialTotalValue := 0.0
	for _, propertyClassResult := range c.propertyClassResults {
		if slices.Contains([]string{"FARMLAND", "RESIDENTIAL"}, propertyClassResult.PropertyClass) {
			residentialTotalValue += propertyClassResult.PropertyTax * 100.0 / c.residentialRate
		} else {
			nonResidentialTotalValue += propertyClassResult.PropertyTax * 100.0 / c.nonResidentialRate
		}
	}
	totalValue := residentialTotalValue + nonResidentialTotalValue
	slog.InfoContext(ctx.Context, "IndexPage: original values", "apartmentTotalValue", printer.Sprintf("$%.0f", apartmentTotalValue), "residentialTotalValue", printer.Sprintf("$%.0f", residentialTotalValue), "nonResidentialTotalValue", printer.Sprintf("$%.0f", nonResidentialTotalValue))
	slog.InfoContext(ctx.Context, "IndexPage: total value", "totalValue", printer.Sprintf("$%.0f", totalValue))

	testRevenue := c.residentialRate/100.0*residentialTotalValue + c.nonResidentialRateMultiplier*c.residentialRate/100.0*nonResidentialTotalValue
	slog.InfoContext(ctx.Context, "IndexPage: revenue", "oldRevenue", printer.Sprintf("$%.0f", c.totalTax), "testRevenue", printer.Sprintf("$%.0f", testRevenue))

	//newApartmentTotalTax := 100.0 * apartmentTotalValue * c.residentialRate / 100.0 / 100.0
	//apartmentTaxDeficit := apartmentTotalTax - newApartmentTotalTax

	//residentialShareOfDeficit := residentialTotalValue / (residentialTotalValue + nonResidentialTotalValue) * apartmentTaxDeficit
	//nonResidentialShareOfDeficit := nonResidentialTotalValue / (residentialTotalValue + nonResidentialTotalValue) * apartmentTaxDeficit

	newResidentialTotalValue := residentialTotalValue
	newNonResidentialTotalValue := nonResidentialTotalValue
	if c.proposedApartmentsAreResidential {
		newResidentialTotalValue += apartmentTotalValue
		newNonResidentialTotalValue -= apartmentTotalValue
	}

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

	newMultiplier := (c.totalTax*100.0/c.residentialRate - newResidentialTotalValue) / newNonResidentialTotalValue
	slog.InfoContext(ctx.Context, "IndexPage: new multiplier", "newMultiplier", fmt.Sprintf("%.4f", newMultiplier))
	slog.InfoContext(ctx.Context, "IndexPage: new rates", "residentialRate", fmt.Sprintf("%.4f", c.residentialRate), "nonResidentialRate", fmt.Sprintf("%.4f", c.residentialRate*newMultiplier))

	if newMultiplier <= c.maximumRateMultiplier {
		newRevenue := c.residentialRate/100.0*newResidentialTotalValue + newMultiplier*c.residentialRate/100.0*newNonResidentialTotalValue
		slog.InfoContext(ctx.Context, "IndexPage: revenue", "oldRevenue", printer.Sprintf("$%.0f", c.totalTax), "newRevenue", printer.Sprintf("$%.0f", newRevenue))

		c.proposedNonResidentialRateMultiplier = newMultiplier
		c.proposedResidentialRate = c.residentialRate
	} else {
		c.proposedNonResidentialRateMultiplier = c.maximumRateMultiplier
		c.proposedResidentialRate = c.totalTax * 100.0 / (newResidentialTotalValue + newNonResidentialTotalValue*c.maximumRateMultiplier)
	}
	c.proposedNonResidentialRate = c.proposedResidentialRate * c.maximumRateMultiplier

	ctx.Update()
}

func (c *IndexPage) calculateProposedPropertyClassResults(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: calculateProposedPropertyClassResults")

	if c.selectedSchoolDistrict == "" {
		return
	}

	{
		query := c.proposedQuery(`
SELECT
parcel.property_class,
COUNT(*) AS property_count,
SUM(parcelvalue2025.school_taxable) AS property_value,
SUM(parceltax2025.total) AS property_tax,
AVG(parcelvalue2025.school_taxable) AS average_value,
AVG(parceltax2025.total) AS average_tax
FROM parcel
INNER JOIN parceltax2025 USING(parcelid)
INNER JOIN parcelvalue2025 USING(parcelid)
WHERE 1
AND parcel.district = ?
AND parcel.property_class NOT LIKE '%exempt%'
GROUP BY parcel.property_class
`)
		var propertyClassResults []PropertyClassResult
		err := c.db.Raw(query, c.selectedSchoolDistrict).
			Scan(&propertyClassResults).
			Error
		if err != nil {
			slog.ErrorContext(ctx.Context, "IndexPage: Error executing query", "err", err)
			return
		}

		c.proposedPropertyClassResults = propertyClassResults
	}

	c.proposedTotalTax = 0
	for _, propertyClassResult := range c.proposedPropertyClassResults {
		c.proposedTotalTax += propertyClassResult.PropertyTax
	}

	ctx.Update()
}
