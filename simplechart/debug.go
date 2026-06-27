package simplechart

// Debug toggles debug logging for this package.
func Debug(debug bool) {
	debugSimpleChart = debug
}

// debugSimpleChart toggles debug logging for AppBar.
var debugSimpleChart bool = false

// DebugSimpleChart toggles debug logging for AppBar.
func DebugSimpleChart(debug bool) {
	debugSimpleChart = debug
}
