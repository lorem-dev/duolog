package config

type Telemetry struct {
	Enabled         bool `yaml:"enabled" jsonschema_description:"Keep local telemetry on this machine; defaults to true. User level; a project may set false to stop its own run, gate and peer events, and DUOLOG_TELEMETRY=off turns it off everywhere."`
	RetentionDays   int  `yaml:"retention_days" jsonschema_description:"Days a telemetry record lives, 1 to 30; defaults to 30. User level only; project layers cannot change it." jsonschema:"minimum=1,maximum=30"`
	PruneEveryHours int  `yaml:"prune_every_hours" jsonschema_description:"Hours between prunes triggered by a telemetry write; defaults to 6. User level only." jsonschema:"minimum=1"`
	MaxMegabytes    int  `yaml:"max_megabytes" jsonschema_description:"Size cap of the whole telemetry store in megabytes, oldest segments removed first; defaults to 16. User level only." jsonschema:"minimum=1"`
	ProjectEnabled  bool `yaml:"-" jsonschema:"-"`
}
