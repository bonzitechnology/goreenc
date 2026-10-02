package main

import (
	"fmt"
	"os"
	"time"

	"github.com/kronicd/goreenc/internal/config"
	"github.com/kronicd/goreenc/internal/encoder"
	"github.com/kronicd/goreenc/internal/logger"
	"github.com/kronicd/goreenc/internal/scanner"
	"github.com/kronicd/goreenc/internal/stats"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Parse command line arguments
	cfg, err := parseArgs()
	if err != nil {
		return err
	}

	// Setup logger
	logLevel := logger.LevelInfo
	if cfg.Debug {
		logLevel = logger.LevelDebug
	} else if cfg.Verbose {
		logLevel = logger.LevelInfo
	}
	log := logger.New(logLevel)

	// Setup log file if requested
	if cfg.LogFile != "" {
		if err := log.SetLogFile(cfg.LogFile); err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}
		defer log.Close()
	}

	// Print banner
	log.Info("goenc - Go Video Encoder")
	log.Info("========================")
	log.Info("")

	if cfg.DryRun {
		log.Info("⚠ DRY-RUN MODE - No files will be modified")
		log.Info("")
	}

	// Setup stats writer if requested
	var statsWriter *stats.Writer
	if cfg.StatsCSV != "" {
		statsWriter, err = stats.New(cfg.StatsCSV)
		if err != nil {
			return fmt.Errorf("failed to create stats file: %w", err)
		}
		defer statsWriter.Close()
	}

	// Create encoder
	encOpts := encoder.Options{
		DryRun:             cfg.DryRun,
		Delete:             cfg.Delete,
		Override:           cfg.Override,
		TempDir:            cfg.TempDir,
		IgnoreProcessed:    cfg.IgnoreProcessed,
		RetryFailed:        cfg.RetryFailed,
		UseAV1:             cfg.UseAV1,
		Keep4K:             cfg.Keep4K,
		PreserveTimestamps: cfg.PreserveTimestamps,
		Quality:            cfg.Quality,
		Preset:             cfg.Preset,
	}
	enc := encoder.New(log, encOpts)
	defer enc.Close()

	// Track stats
	var processed, replaced, discarded, skipped, failed int
	var totalSaved int64
	startTime := time.Now()

	// Scan for video files and process as they're found
	log.Info("Scanning and processing: %s", cfg.InputPath)
	log.Info("")

	scanOpts := scanner.ScanOptions{
		MinSizeMB: cfg.SizeThresholdMB,
		Override:  cfg.Override,
	}

	videoChan, errChan := scanner.ScanStream(cfg.InputPath, scanOpts)
	fileCount := 0

	for {
		select {
		case file, ok := <-videoChan:
			if !ok {
				// Channel closed, all files processed
				videoChan = nil
				continue
			}

			fileCount++
			log.Info("[%d] %s", fileCount, file.Path)

			result, err := enc.Encode(file.Path)
			if err != nil {
				log.Error("Failed to encode: %v", err)
				failed++
			} else {
				processed++

				switch result.Status {
				case "replaced":
					replaced++
					totalSaved += result.Saved
				case "discarded":
					discarded++
				case "skipped":
					skipped++
				case "encoded":
					replaced++
					totalSaved += result.Saved
				}
			}

			// Write to stats CSV
			if statsWriter != nil && result != nil {
				if err := statsWriter.WriteResult(result); err != nil {
					log.Warn("Failed to write stats: %v", err)
				}
			}

			log.Info("")

		case err := <-errChan:
			if err != nil {
				return fmt.Errorf("scan error: %w", err)
			}
		}

		// Exit when both channels are closed
		if videoChan == nil && len(errChan) == 0 {
			break
		}
	}

	if fileCount == 0 {
		log.Info("No video files found matching criteria")
		return nil
	}

	// Print summary
	elapsed := time.Since(startTime)
	log.Info("========================")
	log.Info("Summary:")
	log.Info("  Files processed: %d", processed)
	log.Info("  Files replaced:  %d", replaced)
	log.Info("  Files discarded: %d", discarded)
	log.Info("  Files skipped:   %d", skipped)
	log.Info("  Files failed:    %d", failed)
	log.Info("  Total saved:     %s", scanner.FormatSize(totalSaved))
	log.Info("  Time elapsed:    %s", elapsed.Round(time.Second))

	if cfg.StatsCSV != "" {
		log.Info("  Stats exported:  %s", cfg.StatsCSV)
	}

	return nil
}

func parseArgs() (*config.Config, error) {
	cfg := config.NewDefault()

	// Simple argument parsing (can be replaced with cobra later if needed)
	args := os.Args[1:]

	if len(args) == 0 {
		printUsage()
		os.Exit(0)
	}

	// Parse flags
	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch arg {
		case "-h", "--help":
			printUsage()
			os.Exit(0)
		case "-d", "--dry-run":
			cfg.DryRun = true
		case "-w", "--wetrun":
			cfg.DryRun = false
		case "-s", "--size-threshold":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--size-threshold requires a value")
			}
			i++
			fmt.Sscanf(args[i], "%d", &cfg.SizeThresholdMB)
		case "-o", "--override":
			cfg.Override = true
		case "--delete":
			cfg.Delete = true
		case "--av1":
			cfg.UseAV1 = true
		case "--4k":
			cfg.Keep4K = true
		case "--quality":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--quality requires a value")
			}
			i++
			fmt.Sscanf(args[i], "%d", &cfg.Quality)
		case "--preset":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--preset requires a value")
			}
			i++
			cfg.Preset = args[i]
		case "--preserve-timestamps":
			cfg.PreserveTimestamps = true
		case "--ignore-processed":
			cfg.IgnoreProcessed = true
		case "--retry-failed":
			cfg.RetryFailed = true
		case "--stats-csv":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--stats-csv requires a value")
			}
			i++
			cfg.StatsCSV = args[i]
		case "-v", "--verbose":
			cfg.Verbose = true
		case "--debug":
			cfg.Debug = true
		case "--log":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--log requires a value")
			}
			i++
			cfg.LogFile = args[i]
		default:
			// Assume it's the input path
			if cfg.InputPath == "" {
				cfg.InputPath = arg
			} else {
				return nil, fmt.Errorf("unknown flag or multiple paths: %s", arg)
			}
		}
	}

	if cfg.InputPath == "" {
		return nil, fmt.Errorf("no input path specified")
	}

	return cfg, nil
}

func printUsage() {
	fmt.Println("goenc - Go Video Encoder")
	fmt.Println()
	fmt.Println("Usage: goenc [flags] <path>")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  -d, --dry-run              Show what would be processed (default)")
	fmt.Println("  -w, --wetrun               Actually perform encoding")
	fmt.Println("  -s, --size-threshold <MB>  Minimum file size in MB (default: 500)")
	fmt.Println("  -o, --override             Re-encode files already in HEVC/AV1")
	fmt.Println("      --delete               Delete original after successful encode")
	fmt.Println("      --av1                  Use AV1 codec instead of HEVC")
	fmt.Println("      --4k                   Keep 4K resolution (disable downscaling)")
	fmt.Println("      --quality <crf>        Override CRF quality (e.g. 18–28; lower = better)")
	fmt.Println("      --preset <preset>      Override encoder preset (e.g. slow, medium, fast)")
	fmt.Println("      --preserve-timestamps  Preserve original file modification times")
	fmt.Println("      --ignore-processed     Ignore goenc metadata (re-process success/discarded)")
	fmt.Println("      --retry-failed         Retry files that previously failed encoding")
	fmt.Println("      --stats-csv <file>     Output stats to CSV file")
	fmt.Println("      --log <file>           Log output to file (appends)")
	fmt.Println("  -v, --verbose              Verbose logging")
	fmt.Println("      --debug                Debug logging")
	fmt.Println("  -h, --help                 Show this help")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  goenc /media/videos                    # Dry-run scan")
	fmt.Println("  goenc --wetrun /media/videos           # Actually encode")
	fmt.Println("  goenc --wetrun --delete /media/videos  # Encode and replace originals")
	fmt.Println("  goenc --wetrun --retry-failed /media   # Retry previously failed files")
}
