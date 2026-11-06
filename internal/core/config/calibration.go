package config

type Calibration struct {
	MaxWeeklyShare float64           `yaml:"max_weekly_share" jsonschema_description:"Most of each agent's weekly window the whole calibration may spend, above 0 and at most 1; defaults to 0.10."`
	MaxWindowShare float64           `yaml:"max_window_share" jsonschema_description:"Most of a five-hour window calibration may spend before it pauses until the window resets, above 0 and at most 0.5; defaults to 0.5."`
	CellMaxMinutes int               `yaml:"cell_max_minutes" jsonschema_description:"Time cap for one cell in minutes; a task estimated above it is excluded. Defaults to 45."`
	CellMaxTokens  int64             `yaml:"cell_max_tokens" jsonschema_description:"Token cap for one cell; a task estimated above it is excluded, and a cell whose reported usage is above it fails with reason token cap and gives no result. Defaults to 1500000."`
	CellMaxTurns   int               `yaml:"cell_max_turns" jsonschema_description:"Turn cap for one cell; defaults to 100."`
	HistoryDays    int               `yaml:"history_days" jsonschema_description:"Days of history mined first, widened when a class has fewer than 3 tasks; defaults to 90."`
	Filter         CalibrationFilter `yaml:"filter" jsonschema_description:"Size limits for commits mined as tasks, after dropping generated and vendored files."`
	// Ignored names each cap of the shared .duolog/config.yaml that Load
	// ignored because it was above the layers below it.
	Ignored []string `yaml:"-" jsonschema:"-"`
}

type CalibrationFilter struct {
	MinFiles      int `yaml:"min_files" jsonschema_description:"Fewest changed files for a task; defaults to 1."`
	MaxFiles      int `yaml:"max_files" jsonschema_description:"Most changed files for a task; defaults to 20."`
	MinLines      int `yaml:"min_lines" jsonschema_description:"Fewest changed lines for a task; defaults to 10."`
	MaxLines      int `yaml:"max_lines" jsonschema_description:"Most changed lines for a task; defaults to 400."`
	LargeMaxFiles int `yaml:"large_max_files" jsonschema_description:"Most changed files for the larger stratum, used only when the project's work is large; defaults to 40."`
	LargeMaxLines int `yaml:"large_max_lines" jsonschema_description:"Most changed lines for the larger stratum, used only when the project's work is large; defaults to 1500."`
}
