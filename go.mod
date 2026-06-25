module github.com/tekkamanendless/csd-tax-parcel-analysis

go 1.26.2

require (
	github.com/WinterYukky/gorm-extra-clause-plugin v0.4.0
	github.com/go-app-blazar/blazar v0.1.13
	github.com/go-app-blazar/router v0.1.0
	github.com/joho/godotenv v1.5.1
	github.com/lmittmann/tint v1.1.3
	github.com/mattn/go-isatty v0.0.22
	github.com/maxence-charriere/go-app/v11 v11.0.4
	github.com/ncruces/go-sqlite3 v0.34.0
	github.com/ncruces/go-sqlite3/gormlite v0.34.0
	github.com/tekkamanendless/gormslog v0.1.1
	golang.org/x/text v0.37.0
	gorm.io/gorm v1.31.1
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/ncruces/go-sqlite3-wasm/v2 v2.1.35300 // indirect
	github.com/ncruces/julianday v1.0.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
)

replace github.com/maxence-charriere/go-app/v11 => github.com/tekkamanendless/fork-of-maxence-charriere-go-app/v11 v11.0.0-20260624062618-c2f3e531f36d
