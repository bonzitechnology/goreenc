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
	Target             string // Playback target: "" (1080p), "4k" or "8k"
	PreserveTimestamps bool // Preserve original file timestamps
	MinSavings         float64 // Minimum saving (percent) required to keep an encode
	Estimate           bool    // Estimate savings with sample encodes first
	DropUnsupported    bool    // Drop streams MKV can't hold instead of skipping the file

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
		Quality:         0,        // 0 = use profile default (based on resolution)
		Preset:          "",       // "" = use profile default
		Override:        false,
		DryRun:          true, // Default to dry-run for safety
		Delete:          false,
		Watch:           false,
		Concurrency:     1,
		Verbose:         false,
		Debug:           false,
	}
}
