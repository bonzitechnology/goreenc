package config

// Config holds all configuration options
type Config struct {
	// Paths
	InputPath string
	TempDir   string
	StatsCSV  string

	// Encoding options
	SizeThresholdMB    int64
	Quality            int
	Preset             string
	Override           bool
	UseAV1             bool // Use AV1 instead of HEVC
	Keep4K             bool // Disable downscaling, keep original resolution
	PreserveTimestamps bool // Preserve original file timestamps

	// Operation modes
	DryRun          bool
	Delete          bool
	Watch           bool
	IgnoreProcessed bool // Ignore goenc metadata for success/discarded files
	RetryFailed     bool // Retry files that previously failed

	// Concurrency
	Concurrency int

	// Logging
	Verbose bool
	Debug   bool
	LogFile string
}

// NewDefault returns a Config with default values
func NewDefault() *Config {
	return &Config{
		TempDir:         "/tmp/goenc",
		SizeThresholdMB: 500,
		Quality:         0, // Auto-detect based on resolution
		Preset:          "medium",
		Override:        false,
		DryRun:          true, // Default to dry-run for safety
		Delete:          false,
		Watch:           false,
		Concurrency:     1,
		Verbose:         false,
		Debug:           false,
	}
}
