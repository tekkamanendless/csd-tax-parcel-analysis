package demo

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"io/fs"
	"log/slog"
	"strings"

	"github.com/go-app-blazar/blazar/blazar"
	"github.com/maxence-charriere/go-app/v11/pkg/app"
	"github.com/ncruces/go-sqlite3/util/ioutil"
	"github.com/ncruces/go-sqlite3/vfs/readervfs"
	"github.com/tekkamanendless/csd-tax-parcel-analysis/dataset"
	"github.com/tekkamanendless/csd-tax-parcel-analysis/internal/database"
	"gorm.io/gorm"
)

type IndexPage struct {
	app.Compo

	db              *gorm.DB
	schoolDistricts []string
	districtRates   []DistrictRate
	countyRates     []CountyRate

	maximumRateRatio             float64
	selectedSchoolDistrict       string
	nonResidentialRateMultiplier float64
	residentialRate              float64
	nonResidentialRate           float64
	apartmentsAreResidential     bool

	propertyClassResults []PropertyClassResult
}

func (c *IndexPage) queryPrefix() string {
	residentialPropertyClasses := []string{
		"FARMLAND",
		"RESIDENTIAL",
	}
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
	return strings.TrimSpace(`
	WITH
countyrates AS (SELECT 0.21 AS votech2024, 0.0431 AS votech2025),
districtrates AS (SELECT 'christina' AS district, 3.224 AS rate2024, 0.6150 AS rate2025, 1.2102 AS nrate2025 UNION SELECT 'brandywine' AS district, 2.7685 AS rate2024, 0.5609 AS rate2025, 1.0382 AS nrate2025 UNION SELECT 'colonial' AS district, 2.296 AS rate2024, 0.4523 AS rate2025, 0.74294 AS nrate2025 UNION SELECT 'redclay' AS district, 2.658 AS rate2024, 0.5918 AS rate2025, 0.99237 AS nrate2025 UNION SELECT 'appoquinimink' AS district, 3.1454 AS rate2024, 0.57692 AS rate2025, 1.15378 AS nrate2025),
credit2025 AS ( SELECT parceltax.parcelid AS parcelid, CAST(100 * ( parcelassessment.school_taxable * (districtrates.rate2025 + countyrates.votech2025)/100 - ( school_amount_paid + school_principal_due ) ) AS INT) / 100 AS credit FROM parcel INNER JOIN parceltax USING(parcelid) INNER JOIN parcelassessment ON parceltax.parcelid = parcelassessment.parcelid AND parcelassessment.type = 'current' INNER JOIN districtrates USING(district) CROSS JOIN countyrates WHERE parceltax.year = '2025A' ),
parceltax2025 AS ( SELECT parcelassessment.parcelid AS parcelid, CAST(100 * parcelassessment.school_taxable * CASE WHEN parcel.property_class IN ('`+strings.Join(residentialPropertyClasses, "', '")+`') THEN districtrates.rate2025 ELSE districtrates.nrate2025 END/100 AS INT) / 100 + credit2025.credit AS total FROM parcel INNER JOIN parcelassessment USING(parcelid) INNER JOIN credit2025 USING(parcelid) INNER JOIN districtrates USING(district) CROSS JOIN countyrates WHERE parcelassessment.type = 'current' ),
parcelvalue2025 AS ( SELECT parcelid, county_taxable, school_taxable, school_taxable AS total FROM parcelassessment INNER JOIN parcel USING(parcelid) WHERE type = 'current' )
	`) + "\n"
}

func (c *IndexPage) query(query string) string {
	output := c.queryPrefix() + query
	slog.InfoContext(context.TODO(), "Query", "query", output)
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

	subFS, err := fs.Sub(dataset.EmbeddedFS, "embedded")
	if err != nil {
		slog.ErrorContext(ctx.Context, "Error creating sub FS", "err", err)
		return
	}

	file, err := subFS.Open("database.county.sqlite.gz")
	if err != nil {
		slog.ErrorContext(ctx.Context, "Error opening file", "err", err)
		return
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		slog.ErrorContext(ctx.Context, "Error creating gzip reader", "err", err)
		return
	}

	contents, err := io.ReadAll(gzipReader)
	if err != nil {
		slog.ErrorContext(ctx.Context, "Error reading file", "err", err)
		return
	}
	readervfs.Create("database.county.sqlite", ioutil.NewSizeReaderAt(bytes.NewReader(contents)))

	db, err := database.New(ctx.Context, "sqlite3", "file:database.county.sqlite?vfs=reader&cache=shared&parseTime=true")
	if err != nil {
		slog.ErrorContext(ctx.Context, "Error creating database", "err", err)
		return
	}

	err = db.Exec("PRAGMA temp_store = memory;").Error

	c.db = db

	var districtRates []DistrictRate
	err = c.db.Raw(c.query("SELECT district, rate2024, rate2025, nrate2025 FROM districtrates")).
		Scan(&districtRates).
		Error
	if err != nil {
		slog.ErrorContext(ctx.Context, "Error executing query", "err", err)
		return
	}
	c.districtRates = districtRates

	var countyRates []CountyRate
	err = c.db.Raw(c.query("SELECT votech2024, votech2025 FROM countyrates")).
		Scan(&countyRates).
		Error
	if err != nil {
		slog.ErrorContext(ctx.Context, "Error executing query", "err", err)
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
							slog.InfoContext(ctx.Context, "School district changed", "selectedSchoolDistrict", c.selectedSchoolDistrict)

							c.updateForNewSchoolDistrict(ctx)
						}),
				),
			app.Div().
				Body(
					blazar.Input[float64]().
						Label("Non-Residential Rate Multiplier").
						Bind(&c.nonResidentialRateMultiplier).
						On("change", func(ctx app.Context, e app.Event) {
							slog.InfoContext(ctx.Context, "Non-Residential Rate Multiplier changed", "nonResidentialRateMultiplier", c.nonResidentialRateMultiplier)

							c.updateForNewSchoolDistrict(ctx)
						}),
					blazar.Input[float64]().
						Label("Residential Rate").
						Bind(&c.residentialRate).
						On("change", func(ctx app.Context, e app.Event) {
							slog.InfoContext(ctx.Context, "Residential Rate changed", "residentialRate", c.residentialRate)

							c.updateForNewSchoolDistrict(ctx)
						}),
					blazar.Input[float64]().
						Label("Non-Residential Rate").
						Bind(&c.nonResidentialRate).
						On("change", func(ctx app.Context, e app.Event) {
							slog.InfoContext(ctx.Context, "Non-Residential Rate changed", "nonResidentialRate", c.nonResidentialRate)

							c.updateForNewSchoolDistrict(ctx)
						}),
					blazar.Input[bool]().
						Label("Apartments are Residential").
						Bind(&c.apartmentsAreResidential).
						On("change", func(ctx app.Context, e app.Event) {
							slog.InfoContext(ctx.Context, "Apartments are Residential changed", "apartmentsAreResidential", c.apartmentsAreResidential)

							c.updateForNewSchoolDistrict(ctx)
						}),
				),
			app.Div().
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
									return row.PropertyCount
								},
							},
							{
								Name: "Property Value",
								Value: func(row PropertyClassResult) any {
									return row.PropertyValue
								},
							},
							{
								Name: "Property Tax",
								Value: func(row PropertyClassResult) any {
									return row.PropertyTax
								},
							},
							{
								Name: "Average Value",
								Value: func(row PropertyClassResult) any {
									return row.AverageValue
								},
							},
							{
								Name: "Average Tax",
								Value: func(row PropertyClassResult) any {
									return row.AverageTax
								},
							},
							{
								Name: "Median Value",
								Value: func(row PropertyClassResult) any {
									return row.MedianValue
								},
							},
							{
								Name: "Median Tax",
								Value: func(row PropertyClassResult) any {
									return row.MedianTax
								},
							},
						}),
				),
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
			slog.ErrorContext(ctx.Context, "Error executing query", "err", err)
			return
		}

		c.propertyClassResults = propertyClassResults
	}

	ctx.Update()
}
