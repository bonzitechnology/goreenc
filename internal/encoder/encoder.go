package encoder

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kronicd/goreenc/internal/logger"
	"github.com/kronicd/goreenc/internal/probe"
	"github.com/kronicd/goreenc/internal/scanner"
)

// Options holds encoding options
type Options struct {
	DryRun             bool
	Delete             bool
	Override           bool
	TempDir            string
	IgnoreProcessed    bool // Ignore goenc_status metadata (re-process success/discarded)
	RetryFailed        bool // Retry files that previously failed
	UseAV1             bool // Use AV1 instead of HEVC
	Keep4K             bool // Disable downscaling, keep original resolution
	PreserveTimestamps bool // Preserve original file timestamps
	Quality            int  // CRF override (0 = use profile default)
	Preset             string // Preset override ("" = use profile default)
}

// Result represents the outcome of encoding a file
type Result struct {
	InputPath      string
	OutputPath     string
	OriginalSize   int64
	EncodedSize    int64
	Saved          int64
	SavedPercent   float64
	Resolution     string
	Duration       float64
	Status         string // "replaced", "discarded", "skipped", "failed"
	Error          error
	EncodeDuration time.Duration
}

// Encoder handles the encoding process
type Encoder struct {
	log  *logger.Logger
	opts Options
}

// New creates a new Encoder
func New(log *logger.Logger, opts Options) *Encoder {
	// Set default temp dir if not specified
	if opts.TempDir == "" {
		opts.TempDir = "/tmp/goenc"
	}

	return &Encoder{
		log:  log,
		opts: opts,
	}
}

// Encode encodes a single video file
func (e *Encoder) Encode(inputPath string) (*Result, error) {
	result := &Result{
		InputPath: inputPath,
		Status:    "failed",
	}

	// Get metadata
	metadata, err := probe.GetMetadata(inputPath)
	if err != nil {
		result.Error = fmt.Errorf("failed to get metadata: %w", err)
		return result, result.Error
	}

	// Get file info
	fileInfo, err := os.Stat(inputPath)
	if err != nil {
		result.Error = fmt.Errorf("failed to stat file: %w", err)
		return result, result.Error
	}
	result.OriginalSize = fileInfo.Size()

	// Get resolution
	width, height := metadata.GetResolution()
	result.Resolution = fmt.Sprintf("%dx%d", width, height)

	// Get duration
	duration, _ := metadata.GetDuration()
	result.Duration = duration

	// Get stream counts
	v, a, s, _ := metadata.GetStreamCounts()
	codec := metadata.GetVideoCodec()

	// Check if already processed by goenc
	goencStatus := metadata.GetGoencStatus()
	goencTimestamp := metadata.GetGoencTimestamp()

	// Log input info
	e.log.Info("Processing: %s", inputPath)
	e.log.Info("  Original: %s | %s | %s | %dv/%da/%ds",
		scanner.FormatSize(result.OriginalSize),
		result.Resolution,
		codec,
		v, a, s,
	)

	// Check goenc metadata
	if goencStatus != probe.StatusNone && goencStatus != "" {
		switch goencStatus {
		case probe.StatusSuccess:
			if !e.opts.IgnoreProcessed {
				e.log.Info("  ⊘ Already processed by goenc (success at %s)", goencTimestamp)
				e.log.Info("     Use --ignore-processed to re-process")
				result.Status = "skipped"
				return result, nil
			}
		case probe.StatusDiscarded:
			if !e.opts.IgnoreProcessed {
				e.log.Info("  ⊘ Previously discarded by goenc (larger at %s)", goencTimestamp)
				e.log.Info("     Use --ignore-processed to retry")
				result.Status = "skipped"
				return result, nil
			}
		case probe.StatusFailed:
			if !e.opts.RetryFailed {
				e.log.Info("  ⊘ Previously failed encoding at %s", goencTimestamp)
				e.log.Info("     Use --retry-failed to retry")
				result.Status = "skipped"
				return result, nil
			} else {
				e.log.Info("  ⟲ Retrying previously failed file...")
			}
		}
	}

	// Check if already encoded with target codec
	if e.opts.UseAV1 {
		if metadata.IsAV1() && !e.opts.Override {
			e.log.Info("  ⊘ Already AV1 (use --override to re-encode)")
			result.Status = "skipped"
			return result, nil
		}
	} else {
		if metadata.IsHEVC() && !e.opts.Override {
			e.log.Info("  ⊘ Already HEVC (use --override to re-encode)")
			result.Status = "skipped"
			return result, nil
		}
	}

	// Get encoding profile
	profile := GetProfile(height)

	// Apply quality/preset overrides from options
	if e.opts.Quality > 0 {
		profile.CRF = e.opts.Quality
	}
	if e.opts.Preset != "" {
		profile.Preset = e.opts.Preset
	}

	codecName := "HEVC"
	if e.opts.UseAV1 {
		codecName = "AV1"
	}
	if height > 1080 && !e.opts.Keep4K {
		e.log.Info("  Profile: %s (%s, downscaling %dx%d → 1080p)", profile.String(), codecName, width, height)
	} else {
		e.log.Info("  Profile: %s (%s)", profile.String(), codecName)
	}

	if e.opts.DryRun {
		e.log.Info("  [DRY-RUN] Would encode with profile: %s", profile.String())
		result.Status = "skipped"
		return result, nil
	}

	// Create temp directory
	if err := os.MkdirAll(e.opts.TempDir, 0755); err != nil {
		result.Error = fmt.Errorf("failed to create temp dir: %w", err)
		return result, result.Error
	}

	// Create temp output file
	// Always output as .mkv since HEVC may not be supported in original container
	baseName := filepath.Base(inputPath)
	ext := filepath.Ext(baseName)
	nameWithoutExt := baseName[:len(baseName)-len(ext)]
	tempOutput := filepath.Join(e.opts.TempDir, nameWithoutExt+".mkv")
	result.OutputPath = tempOutput

	// Build ffmpeg command
	cmd := BuildFFmpegCommand(inputPath, tempOutput, metadata, profile, e.opts.UseAV1, e.opts.Keep4K)

	// Log the full command for debugging
	e.log.Debug("  ffmpeg command: %s", cmd.String())

	// Run encoding
	startTime := time.Now()
	totalFrames := metadata.GetTotalFrames()
	if err := e.runFFmpeg(cmd, totalFrames); err != nil {
		result.Error = fmt.Errorf("ffmpeg failed: %w", err)
		result.Status = "failed"

		// Write failed metadata to original file
		e.log.Debug("  Writing failed metadata to original...")
		if metaErr := probe.WriteMetadataPreserveTime(inputPath, probe.StatusFailed, e.opts.PreserveTimestamps); metaErr != nil {
			e.log.Warn("  Failed to write metadata: %v", metaErr)
		}

		os.Remove(tempOutput) // Clean up temp file
		return result, result.Error
	}
	result.EncodeDuration = time.Since(startTime)

	e.log.ProgressDone() // Clear progress line

	// Get encoded file size
	encodedInfo, err := os.Stat(tempOutput)
	if err != nil {
		result.Error = fmt.Errorf("failed to stat encoded file: %w", err)
		os.Remove(tempOutput)
		return result, result.Error
	}
	result.EncodedSize = encodedInfo.Size()

	// Calculate savings
	result.Saved = result.OriginalSize - result.EncodedSize
	result.SavedPercent = (float64(result.Saved) / float64(result.OriginalSize)) * 100

	// Log result
	if result.Saved > 0 {
		e.log.Info("  Encoded: %s | Saved %s (%.1f%%)",
			scanner.FormatSize(result.EncodedSize),
			scanner.FormatSize(result.Saved),
			result.SavedPercent,
		)
	} else {
		e.log.Info("  Encoded: %s | Wasted %s (+%.1f%%)",
			scanner.FormatSize(result.EncodedSize),
			scanner.FormatSize(-result.Saved),
			-result.SavedPercent,
		)
	}

	// Decide whether to replace or discard
	if result.Saved > 0 {
		// Encoded file is smaller - replace original
		if e.opts.Delete {
			// Get original file timestamps before doing anything
			var originalModTime time.Time
			if e.opts.PreserveTimestamps {
				if info, err := os.Stat(inputPath); err == nil {
					originalModTime = info.ModTime()
				}
			}

			// Calculate final output path (replace extension with .mkv)
			finalPath := nameWithoutExt + ".mkv"
			if filepath.Dir(inputPath) != "." {
				finalPath = filepath.Join(filepath.Dir(inputPath), finalPath)
			}

			// Copy temp file to final location (can't use Rename across filesystems)
			if err := probe.CopyFile(tempOutput, finalPath); err != nil {
				result.Error = fmt.Errorf("failed to copy encoded file: %w", err)
				os.Remove(tempOutput)
				return result, result.Error
			}

			// Write success metadata to new file
			e.log.Debug("  Writing success metadata...")
			if err := probe.WriteMetadata(finalPath, probe.StatusSuccess); err != nil {
				e.log.Warn("  Failed to write metadata: %v", err)
			}

			// If metadata write failed but we still want to preserve timestamp, do it now
			if e.opts.PreserveTimestamps && !originalModTime.IsZero() {
				if err := os.Chtimes(finalPath, originalModTime, originalModTime); err != nil {
					e.log.Warn("  Failed to preserve timestamp: %v", err)
				}
			}

			// Only now remove the original file (if different from finalPath)
			if inputPath != finalPath {
				if err := os.Remove(inputPath); err != nil {
					e.log.Warn("  Failed to remove original file: %v", err)
				}
			}

			// Clean up temp file
			os.Remove(tempOutput)

			e.log.Info("  ✓ Replaced original (smaller)")
			result.Status = "replaced"
			result.OutputPath = finalPath
		} else {
			e.log.Info("  ✓ Encode successful (use --delete to replace original)")
			result.Status = "encoded"

			// Write success metadata to temp file (even though we're not replacing)
			// No need to preserve timestamp on temp file
			e.log.Debug("  Writing success metadata to encoded file...")
			if err := probe.WriteMetadata(tempOutput, probe.StatusSuccess); err != nil {
				e.log.Warn("  Failed to write metadata: %v", err)
			}
		}
	} else {
		// Encoded file is larger - discard
		e.log.Info("  ✗ Discarded encode (larger than original)")
		result.Status = "discarded"

		// Write discarded metadata to original file (so we don't try again)
		e.log.Debug("  Writing discarded metadata to original...")
		if err := probe.WriteMetadataPreserveTime(inputPath, probe.StatusDiscarded, e.opts.PreserveTimestamps); err != nil {
			e.log.Warn("  Failed to write metadata: %v", err)
		}

		// Now remove the temp output
		os.Remove(tempOutput)
	}

	return result, nil
}

// runFFmpeg executes ffmpeg and displays progress
func (e *Encoder) runFFmpeg(cmd *exec.Cmd, totalFrames int) error {
	// Capture both stdout and stderr
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	// Start command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Capture all stderr output
	var stderrOutput strings.Builder
	progress := &progressData{} // per-encode state, not shared
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		stderrOutput.WriteString(line + "\n")

		// Parse progress (ffmpeg outputs progress to stderr)
		if info := ParseProgress(line, progress); info != nil {
			if totalFrames > 0 {
				percent := (float64(info.Frame) / float64(totalFrames)) * 100
				e.log.Progress("  Encoding... [frame: %d / %d (%.0f%%) | fps: %.1f | size: %s | time: %s | speed: %s]",
					info.Frame,
					totalFrames,
					percent,
					info.FPS,
					info.Size,
					info.Time,
					info.Speed,
				)
			} else {
				e.log.Progress("  Encoding... [frame: %d | fps: %.1f | size: %s | time: %s | speed: %s]",
					info.Frame,
					info.FPS,
					info.Size,
					info.Time,
					info.Speed,
				)
			}
		}
	}

	// Wait for command to complete
	if err := cmd.Wait(); err != nil {
		// Log full ffmpeg output on error
		e.log.Error("ffmpeg command failed: %s", cmd.String())
		e.log.Error("ffmpeg output:")
		// Log last 30 lines of output for debugging
		lines := strings.Split(stderrOutput.String(), "\n")
		start := len(lines) - 30
		if start < 0 {
			start = 0
		}
		for _, line := range lines[start:] {
			if line != "" {
				e.log.Error("  %s", line)
			}
		}
		return fmt.Errorf("ffmpeg exited with error: %w", err)
	}

	return nil
}

// Close cleans up temp directory
func (e *Encoder) Close() error {
	if e.opts.TempDir != "" && e.opts.TempDir != "/tmp" {
		return os.RemoveAll(e.opts.TempDir)
	}
	return nil
}
