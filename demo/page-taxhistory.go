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
	"time"

	"github.com/go-app-blazar/blazar/blazar"
	"github.com/go-app-blazar/blazar/htmlevent"
	"github.com/maxence-charriere/go-app/v11/pkg/app"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/tekkamanendless/csd-tax-parcel-analysis/dataset"
	"github.com/tekkamanendless/csd-tax-parcel-analysis/internal/database"
	"github.com/tekkamanendless/csd-tax-parcel-analysis/simplechart"
	"gorm.io/gorm"
)

type TaxHistoryPage struct {
	app.Compo

	db *gorm.DB

	loading bool

	schoolDistricts        []string
	taxDistricts           []string
	years                  []int
	selectedSchoolDistrict string
	selectedTaxDistrict    string

	yearResults [][]HistoryPropertyClassResult
}

type HistoryParcel struct {
	ParcelID       string `gorm:"column:parcelid;primaryKey"`
	TaxDistrict    string `gorm:"column:tax_district"`
	SchoolDistrict string `gorm:"column:district;index:district_and_property_class,priority:1"`
	PropertyClass  string `gorm:"column:property_class;index:district_and_property_class,priority:2"`
}

func (HistoryParcel) TableName() string {
	return "parcel"
}

type HistoryParcelHistory struct {
	ParcelID    string  `gorm:"column:parcelid;uniqueIndex:parcelid_year"`
	Year        int     `gorm:"column:year;uniqueIndex:parcelid_year"`
	TotalValue  float64 `gorm:"column:total_value"`
	CountyValue float64 `gorm:"column:county_value"`
	SchoolValue float64 `gorm:"column:school_value"`
}

func (HistoryParcelHistory) TableName() string {
	return "parcel_history"
}

func (c *TaxHistoryPage) OnMount(ctx app.Context) {
	slog.InfoContext(ctx.Context, "TaxHistoryPage: OnMount")

	memdb.Create("my_shared_db", nil)

	db, err := database.New(ctx.Context, "sqlite3", "file:/my_shared_db?vfs=memdb&cache=shared&parseTime=true")
	if err != nil {
		slog.ErrorContext(ctx.Context, "TaxHistoryPage: Error creating database", "err", err)
		return
	}
	err = db.AutoMigrate(&HistoryParcel{}, &HistoryParcelHistory{})
	if err != nil {
		slog.ErrorContext(ctx.Context, "TaxHistoryPage: Error migrating database", "err", err)
		return
	}

	var parcels []HistoryParcel
	var parcelHistories []HistoryParcelHistory
	schoolDistrictMap := map[string]bool{}
	taxDistrictMap := map[string]bool{}
	yearMap := map[int]bool{}
	unhandledAbbrevations := map[string]uint{}
	unhandledTaxDistricts := map[string]uint{}

	subFS, err := fs.Sub(dataset.FS, "embedded")
	if err != nil {
		slog.ErrorContext(ctx.Context, "TaxHistoryPage: Error creating sub FS", "err", err)
		return
	}

	parseParcelFile := func(path string) error {
		file, err := subFS.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		csvReader := csv.NewReader(file)
		rows, err := csvReader.ReadAll()
		if err != nil {
			return err
		}

		header := rows[0]
		slog.InfoContext(ctx.Context, "TaxHistoryPage: Header", "header", header)
		rows = rows[1:]

		headerToIndexMap := map[string]int{}
		for i, header := range header {
			headerToIndexMap[strings.ToLower(header)] = i
		}

		abbrevationToPropertyClassMap := map[string]string{
			"r":  "residential",
			"c":  "commercial",
			"f":  "farmland",
			"ec": "exempt commercial",
			"er": "exempt residential",
			"ef": "exempt farmland",
			"i":  "industrial",
			"u":  "utilities",
			"a":  "apartment",
		}

		taxDistrictToNameMap := map[string]string{
			"08": "City of Newark",
		}

		parcelIDIndex, ok := headerToIndexMap["prclid"]
		if !ok {
			return fmt.Errorf("prclid not found in header")
		}
		taxDistrictIndex, ok := headerToIndexMap["tax district"]
		if !ok {
			return fmt.Errorf("tax district not found in header")
		}
		propertyClassIndex, ok := headerToIndexMap["propcl"]
		if !ok {
			return fmt.Errorf("prop class not found in header")
		}
		schoolDistrictIndex, ok := headerToIndexMap["schooldist"]
		if !ok {
			return fmt.Errorf("descript not found in header")
		}

		type YearIndexes struct {
			TotalValueIndex  int
			CountyValueIndex int
			SchoolValueIndex int
		}
		yearIndexes := map[int]*YearIndexes{}

		for header, index := range headerToIndexMap {
			if strings.HasSuffix(header, " total taxable") {
				yearString := strings.TrimSuffix(header, " total taxable")
				year, err := strconv.Atoi(yearString)
				if err != nil {
					return fmt.Errorf("Error parsing year: %v", err)
				}
				if yearIndexes[year] == nil {
					yearIndexes[year] = &YearIndexes{}
				}
				yearIndexes[year].TotalValueIndex = index
			}
			if strings.HasSuffix(header, " county taxable") {
				yearString := strings.TrimSuffix(header, " county taxable")
				year, err := strconv.Atoi(yearString)
				if err != nil {
					return fmt.Errorf("Error parsing year: %v", err)
				}
				if yearIndexes[year] == nil {
					yearIndexes[year] = &YearIndexes{}
				}
				yearIndexes[year].CountyValueIndex = index
			}
			if strings.HasSuffix(header, " school taxable") {
				yearString := strings.TrimSuffix(header, " school taxable")
				year, err := strconv.Atoi(yearString)
				if err != nil {
					return fmt.Errorf("Error parsing year: %v", err)
				}
				if yearIndexes[year] == nil {
					yearIndexes[year] = &YearIndexes{}
				}
				yearIndexes[year].SchoolValueIndex = index
			}
		}
		for year := range yearIndexes {
			yearMap[year] = true
		}
		slog.InfoContext(ctx.Context, "TaxHistoryPage: Year indexes", "yearIndexes", yearIndexes)

		parcelIDMap := map[string]bool{}

		for _, row := range rows {
			parcel := HistoryParcel{
				ParcelID: row[parcelIDIndex],
			}

			if parcelIDMap[parcel.ParcelID] {
				slog.InfoContext(ctx.Context, "TaxHistoryPage: Duplicate parcel ID", "parcelID", parcel.ParcelID)
				continue
			}
			parcelIDMap[parcel.ParcelID] = true

			if abbreviation := row[propertyClassIndex]; abbreviation != "" {
				parcel.PropertyClass = abbrevationToPropertyClassMap[strings.ToLower(abbreviation)]
				if parcel.PropertyClass == "" {
					unhandledAbbrevations[abbreviation]++
				}
			}
			if taxDistrict := row[taxDistrictIndex]; taxDistrict != "" {
				parcel.TaxDistrict = taxDistrict
				if taxDistrictToNameMap[taxDistrict] != "" {
					parcel.TaxDistrict = taxDistrictToNameMap[taxDistrict]
				} else {
					unhandledTaxDistricts[taxDistrict]++
				}
				taxDistrictMap[parcel.TaxDistrict] = true
			}

			schoolDistrict := strings.ToLower(row[schoolDistrictIndex])
			if strings.Contains(schoolDistrict, "christina") {
				parcel.SchoolDistrict = "christina"
			} else if strings.Contains(schoolDistrict, "brandywine") {
				parcel.SchoolDistrict = "brandywine"
			} else if strings.Contains(schoolDistrict, "colonial") {
				parcel.SchoolDistrict = "colonial"
			} else if strings.Contains(schoolDistrict, "red clay") {
				parcel.SchoolDistrict = "redclay"
			} else if strings.Contains(schoolDistrict, "appoquinimink") {
				parcel.SchoolDistrict = "appoquinimink"
			} else if strings.Contains(schoolDistrict, "smyrna") {
				parcel.SchoolDistrict = "smyrna"
			}

			taxDistrict := strings.ToLower(row[taxDistrictIndex])
			if taxDistrictToNameMap[taxDistrict] != "" {
				parcel.TaxDistrict = taxDistrictToNameMap[taxDistrict]
			} else {
				unhandledTaxDistricts[taxDistrict]++
			}

			parcels = append(parcels, parcel)
			schoolDistrictMap[parcel.SchoolDistrict] = true

			for year, indexes := range yearIndexes {
				parcelHistory := HistoryParcelHistory{
					ParcelID: parcel.ParcelID,
					Year:     year,
				}

				if row[indexes.TotalValueIndex] == "" || row[indexes.TotalValueIndex] == "N/A" {
					parcelHistory.TotalValue = 0
				} else {
					v, err := strconv.ParseUint(row[indexes.TotalValueIndex], 10, 64)
					if err != nil {
						return fmt.Errorf("Error parsing total value: %v", err)
					}
					parcelHistory.TotalValue = float64(v)
				}
				if row[indexes.CountyValueIndex] == "" || row[indexes.CountyValueIndex] == "N/A" {
					parcelHistory.CountyValue = 0
				} else {
					v, err := strconv.ParseUint(row[indexes.CountyValueIndex], 10, 64)
					if err != nil {
						return fmt.Errorf("Error parsing county taxable: %v", err)
					}
					parcelHistory.CountyValue = float64(v)
				}
				if row[indexes.SchoolValueIndex] == "" || row[indexes.SchoolValueIndex] == "N/A" {
					parcelHistory.SchoolValue = 0
				} else {
					v, err := strconv.ParseUint(row[indexes.SchoolValueIndex], 10, 64)
					if err != nil {
						return fmt.Errorf("Error parsing school value: %v", err)
					}
					parcelHistory.SchoolValue = float64(v)
				}
				parcelHistories = append(parcelHistories, parcelHistory)
			}
		}

		return nil
	}

	fs.WalkDir(subFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.ErrorContext(ctx.Context, "TaxHistoryPage: Error walking directory", "err", err)
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(path, "history.") && strings.HasSuffix(path, ".csv") {
			slog.InfoContext(ctx.Context, "TaxHistoryPage: Found CSV file", "path", path)
			err := parseParcelFile(path)
			if err != nil {
				slog.ErrorContext(ctx.Context, "TaxHistoryPage: Error parsing parcel file", "err", err)
				return err
			}
		}
		return nil
	})

	err = db.Transaction(func(db *gorm.DB) error {
		err := db.CreateInBatches(parcels, 2000).Error
		if err != nil {
			return err
		}

		err = db.CreateInBatches(parcelHistories, 2000).Error
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx.Context, "TaxHistoryPage: Error creating parcels", "err", err)
		return
	}

	c.db = db

	c.schoolDistricts = slices.Collect(maps.Keys(schoolDistrictMap))
	slices.Sort(c.schoolDistricts)

	c.taxDistricts = slices.Collect(maps.Keys(taxDistrictMap))
	slices.Sort(c.taxDistricts)

	c.years = slices.Collect(maps.Keys(yearMap))
	slices.Sort(c.years)

	if len(unhandledAbbrevations) > 0 {
		slog.ErrorContext(ctx.Context, "TaxHistoryPage: Unhandled abbreviations", "abbrevations", unhandledAbbrevations)
	}
}

func (c *TaxHistoryPage) OnNav(ctx app.Context) {
	slog.InfoContext(ctx.Context, "TaxHistoryPage: OnNav")
}

func (c *TaxHistoryPage) Render() app.UI {
	yearTotalValueMap := map[int]float64{}
	for _, yearResults := range c.yearResults {
		for _, yearResult := range yearResults {
			yearTotalValueMap[yearResult.Year] += yearResult.PropertyValue
		}
	}

	columns := []blazar.TableColumn[HistoryPropertyClassResult]{
		{
			Name: "Property Class",
			Value: func(row HistoryPropertyClassResult) any {
				return row.PropertyClass
			},
		},
		{
			Name: "Property Count",
			Value: func(row HistoryPropertyClassResult) any {
				return row.PropertyCount
			},
			Type: blazar.TableColumnTypeNumber,
		},
		{
			Name: "Total Value",
			Value: func(row HistoryPropertyClassResult) any {
				return row.PropertyValue
			},
			Format: func(value any) any {
				return fmt.Sprintf("$%v", blazar.TableColumnFormatNumber(value))
			},
			Type: blazar.TableColumnTypeNumber,
		},
		{
			Name: "Percentage",
			Value: func(row HistoryPropertyClassResult) any {
				return 100.0 * row.PropertyValue / yearTotalValueMap[row.Year]
			},
			Format: func(value any) any {
				return fmt.Sprintf("%v%%", blazar.TableColumnFormatNumber(value))
			},
			Type: blazar.TableColumnTypeNumber,
		},
		{
			Name: "Average Value",
			Value: func(row HistoryPropertyClassResult) any {
				if row.PropertyClass == "Total" {
					return nil
				}
				return row.AverageValue
			},
			Format: func(value any) any {
				if value == nil {
					return nil
				}
				return fmt.Sprintf("$%v", blazar.TableColumnFormatNumber(value))
			},
			Type: blazar.TableColumnTypeNumber,
		},
		{
			Name: "Median Value",
			Value: func(row HistoryPropertyClassResult) any {
				if row.PropertyClass == "Total" {
					return nil
				}
				return row.MedianValue
			},
			Format: func(value any) any {
				if value == nil {
					return nil
				}
				return fmt.Sprintf("$%v", blazar.TableColumnFormatNumber(value))
			},
			Type: blazar.TableColumnTypeNumber,
		},
	}

	return blazar.Page().
		Body(
			blazar.Select().
				Label("School District").
				Disabled(c.loading).
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
				Bind(&c.selectedSchoolDistrict),
			blazar.Select().
				Label("Tax District").
				Disabled(c.loading).
				AllowedValue(func() []blazar.SelectOption {
					options := []blazar.SelectOption{
						{
							Value: "",
							Label: "All tax districts",
						},
					}
					for _, district := range c.taxDistricts {
						options = append(options, blazar.SelectOption{
							Value: district,
							Label: district,
						})
					}
					return options
				}()...).
				Bind(&c.selectedTaxDistrict),
			app.If(c.selectedSchoolDistrict != "", func() app.UI {
				return app.Div().
					Body(
						blazar.Button().
							Label("Get Property Values").
							Disabled(c.loading).
							OnClick(func(ctx app.Context, e htmlevent.MouseEvent) {
								slog.InfoContext(ctx.Context, "TaxHistoryPage: Get Property Values clicked 1", "selectedSchoolDistrict", c.selectedSchoolDistrict)

								c.loading = true
								ctx.After(100*time.Millisecond, func(ctx app.Context) {
									c.updateForNewSchoolDistrict(ctx)

									c.loading = false
									ctx.Update()
								})

								slog.InfoContext(ctx.Context, "TaxHistoryPage: Get Property Values clicked 2", "selectedSchoolDistrict", c.selectedSchoolDistrict)
							}),
						app.If(c.loading, func() app.UI {
							return blazar.ProgressBar().
								Indeterminate(true)
						}),
					)
			}),
			app.Div().
				Style("display", "flex").
				Style("flex-direction", "row").
				Style("--simplechart-radius", "10vmin").
				Body(
					app.Range(c.yearResults).Slice(func(i int) app.UI {
						yearResults := c.yearResults[i]

						return app.Div().
							Body(
								app.H3().Text(fmt.Sprintf("Year %d", yearResults[0].Year)),
								simplechart.SimpleChart().
									Label("Property Value").
									Items(func() []simplechart.SimpleChartItem {
										var items []simplechart.SimpleChartItem
										totalValue := 0.0
										for _, propertyClassResult := range yearResults {
											totalValue += propertyClassResult.PropertyValue
										}
										for _, propertyClassResult := range yearResults {
											items = append(items, simplechart.SimpleChartItem{
												Label: propertyClassResult.PropertyClass,
												Value: 100.0 * propertyClassResult.PropertyValue / totalValue,
											})
										}
										return items
									}()),
							)
					}),
				),
			app.Range(c.yearResults).Slice(func(i int) app.UI {
				yearResults := c.yearResults[i]

				return app.Div().
					Body(
						app.H3().Text(fmt.Sprintf("Year %d", yearResults[0].Year)),
						app.Div().
							Style("flex", "1").
							Style("display", "flex").
							Style("flex-direction", "row").
							Style("flex-wrap", "wrap").
							Style("gap", "1em").
							Body(
								simplechart.SimpleChart().
									Label("Property Value").
									Items(func() []simplechart.SimpleChartItem {
										var items []simplechart.SimpleChartItem
										totalValue := 0.0
										for _, propertyClassResult := range yearResults {
											totalValue += propertyClassResult.PropertyValue
										}
										for _, propertyClassResult := range yearResults {
											items = append(items, simplechart.SimpleChartItem{
												Label: propertyClassResult.PropertyClass,
												Value: 100.0 * propertyClassResult.PropertyValue / totalValue,
											})
										}
										return items
									}()),
								blazar.Table[HistoryPropertyClassResult]().
									Style("font-size", "0.9em").
									Rows(yearResults).
									Columns(columns),
							),
					)
			}),
		)
}

func (c *TaxHistoryPage) query(query string) string {
	var queryPrefix string

	// NOTE: We would inject any CTEs here.

	output := queryPrefix + query
	slog.InfoContext(context.TODO(), "TaxHistoryPage: Query", "query", output)
	return output
}

type HistoryPropertyClassResult struct {
	PropertyClass string  `gorm:"column:property_class"`
	Year          int     `gorm:"column:year"`
	PropertyCount int64   `gorm:"column:property_count"`
	PropertyValue float64 `gorm:"column:property_value"`
	AverageValue  float64 `gorm:"column:average_value"`
	MedianValue   float64 `gorm:"column:median_value"`
}

func (c *TaxHistoryPage) updateForNewSchoolDistrict(ctx app.Context) {
	slog.InfoContext(ctx.Context, "TaxHistoryPage: updateForNewSchoolDistrict", "selectedSchoolDistrict", c.selectedSchoolDistrict)

	if c.selectedSchoolDistrict == "" {
		return
	}

	query := c.query(`
SELECT
parcel.property_class,
parcel_history.year,
COUNT(*) AS property_count,
SUM(parcel_history.total_value) AS property_value,
AVG(parcel_history.total_value) AS average_value,
MEDIAN(parcel_history.total_value) AS median_value
FROM parcel
INNER JOIN parcel_history USING(parcelid)
WHERE 1
AND parcel.district = ?
AND IF(? = '', TRUE, parcel.tax_district = ?)
AND parcel.property_class NOT LIKE '%exempt%'
AND parcel_history.total_value > 0
GROUP BY parcel.property_class, parcel_history.year
ORDER BY parcel.property_class, parcel_history.year
`)
	arguments := []any{
		c.selectedSchoolDistrict,
		c.selectedTaxDistrict, c.selectedTaxDistrict,
	}
	propertyClassResults := make([]HistoryPropertyClassResult, 0, 100_000)
	err := c.db.Raw(query, arguments...).
		Find(&propertyClassResults).
		Error
	if err != nil {
		slog.ErrorContext(ctx.Context, "TaxHistoryPage: Error executing query", "err", err)
		return
	}

	ctx.Dispatch(func(ctx app.Context) {
		yearToResultsMap := map[int][]HistoryPropertyClassResult{}
		for _, row := range propertyClassResults {
			yearToResultsMap[row.Year] = append(yearToResultsMap[row.Year], row)
		}
		years := slices.Collect(maps.Keys(yearToResultsMap))
		slices.Sort(years)

		c.yearResults = make([][]HistoryPropertyClassResult, 0, len(c.years))
		for _, year := range years {
			c.yearResults = append(c.yearResults, yearToResultsMap[year])
		}

		ctx.Update()
	})
}
