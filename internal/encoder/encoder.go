package encoder

import (
	"bufio"
	"fmt"
	"math"
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
	Target             Target // Playback resolution and hardware tuning (zero value = 1080p)
	PreserveTimestamps bool // Preserve original file timestamps
	Quality            int  // CRF override (0 = use profile default)
	Preset             string // Preset override ("" = use profile default)
	MinSavings         float64 // Minimum saving (percent of original size) required to keep an encode
	Estimate           bool    // Estimate savings with sample encodes before doing the full encode
	DropUnsupported    bool    // Drop streams MKV can't hold instead of skipping the file
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
	log    *logger.Logger
	opts   Options
	runDir string // per-run directory inside TempDir, created on first encode
}

// New creates a new Encoder
func New(log *logger.Logger, opts Options) *Encoder {
	// Set default temp dir if not specified
	if opts.TempDir == "" {
		opts.TempDir = "/tmp/goenc"
	}
	if opts.Target.Name == "" {
		opts.Target = Target1080p
	}

	return &Encoder{
		log:  log,
		opts: opts,
	}
}

// Encode encodes a single video file
func (e *Encoder) Encode(inputPath string) (*Result, error) {
	// Normalise so "./movie.mkv" and "movie.mkv" compare equal when deciding
	// whether the output path is the input itself
	inputPath = filepath.Clean(inputPath)

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
	if goencStatus == probe.StatusNone {
		// Status that couldn't be embedded is kept in a sidecar file
		goencStatus, goencTimestamp = probe.ReadSidecar(inputPath)
	}

	// Log input info
	e.log.Info("Processing: %s", inputPath)
	e.log.Info("  Original: %s | %s | %s | %dv/%da/%ds",
		scanner.FormatSize(result.OriginalSize),
		result.Resolution,
		codec,
		v, a, s,
	)

	// 3D files are never re-encoded: x265/svt-av1 don't preserve the frame
	// packing signalling (or MVC), so the result no longer plays as 3D
	if is3D(inputPath, metadata) {
		if goencStatus == probe.StatusSkipped3D {
			e.log.Info("  ⊘ 3D video, skipped (tagged at %s)", goencTimestamp)
		} else {
			e.log.Info("  ⊘ 3D video, skipping")
			if !e.opts.DryRun {
				e.recordStatus(inputPath, probe.StatusSkipped3D)
			}
		}
		result.Status = "skipped"
		return result, nil
	}

	// A discard only blocks the codec it was recorded for: a file HEVC couldn't
	// shrink may still benefit from AV1, and vice versa
	if probe.HasStatus(goencStatus, e.discardedStatus()) && !e.opts.IgnoreProcessed {
		e.log.Info("  ⊘ Previously discarded by goenc (no worthwhile saving at %s)", goencTimestamp)
		e.log.Info("     Use --ignore-processed to retry")
		result.Status = "skipped"
		return result, nil
	}

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
		// AV1 is already more efficient than HEVC; transcoding it only loses quality
		if metadata.IsAV1() && !e.opts.Override {
			e.log.Info("  ⊘ Already AV1 (use --override to re-encode to HEVC)")
			result.Status = "skipped"
			return result, nil
		}
	}

	// Dolby Vision profile 5 has no HDR10/SDR base layer: re-encoding drops the
	// DV metadata and leaves IPT-encoded video that plays back green and purple.
	if dv := metadata.GetDolbyVisionProfile(); dv == 5 {
		e.log.Info("  ⊘ Dolby Vision profile 5 cannot be re-encoded without breaking its colors")
		result.Status = "skipped"
		return result, nil
	} else if dv > 0 {
		e.log.Warn("  Dolby Vision (profile %d) layer will be dropped; the HDR10/HLG/SDR base layer is kept", dv)
	}

	// Work out how every stream is carried into the MKV output
	plan, err := planStreams(inputPath, metadata)
	if err != nil {
		result.Error = fmt.Errorf("failed to plan streams: %w", err)
		return result, result.Error
	}
	for _, ps := range plan.Mapped {
		if ps.Action == actionToSRT {
			e.log.Info("  Converting subtitle stream %s to SRT (not supported in MKV as-is)", describe(ps.Stream))
		}
	}
	if len(plan.Unsupported) > 0 {
		var names []string
		for _, s := range plan.Unsupported {
			names = append(names, describe(s))
		}
		if !e.opts.DropUnsupported {
			// Never lose a stream silently: leave the file alone instead
			e.log.Info("  ⊘ Contains streams MKV can't hold: %s", strings.Join(names, ", "))
			e.log.Info("     Use --drop-unsupported to encode without them")
			result.Status = "skipped"
			return result, nil
		}
		e.log.Warn("  Dropping streams MKV can't hold: %s", strings.Join(names, ", "))
	}

	// Get encoding profile
	profile := GetProfile(height, e.opts.UseAV1)

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
	if e.opts.Target.NeedsScale(width, height) {
		e.log.Info("  Profile: %s (%s, downscaling %dx%d → %s)", profile.String(), codecName, width, height, e.opts.Target.Name)
	} else {
		e.log.Info("  Profile: %s (%s)", profile.String(), codecName)
	}

	// Create temp directory
	runDir, err := e.tempRunDir()
	if err != nil {
		result.Error = fmt.Errorf("failed to create temp dir: %w", err)
		return result, result.Error
	}

	// Estimate savings from sample encodes before committing to a full encode
	if e.opts.Estimate {
		estimate, ok, err := e.estimateSavings(inputPath, metadata, profile, result.OriginalSize, duration, runDir)
		switch {
		case err != nil:
			e.log.ProgressDone()
			e.log.Warn("  Savings estimate failed, doing full encode: %v", err)
		case !ok:
			e.log.Debug("  Too short to estimate savings, doing full encode")
		case estimate <= 0 || estimate < e.opts.MinSavings:
			// With no minimum set, the estimate only skips files it expects to grow
			e.log.Info("  ✗ Estimated saving %.1f%% is below the %.1f%% minimum, skipping encode", estimate, e.opts.MinSavings)
			result.Status = "discarded"
			if !e.opts.DryRun {
				e.recordStatus(inputPath, e.newDiscardStatus(goencStatus))
			}
			return result, nil
		default:
			e.log.Info("  Estimated saving: %.1f%%", estimate)
		}
	}

	if e.opts.DryRun {
		e.log.Info("  [DRY-RUN] Would encode with profile: %s", profile.String())
		result.Status = "skipped"
		return result, nil
	}

	// Create temp output file
	// Always output as .mkv since HEVC may not be supported in original container
	baseName := filepath.Base(inputPath)
	nameWithoutExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))
	tempOutput := filepath.Join(runDir, nameWithoutExt+".mkv")
	result.OutputPath = tempOutput

	// Cover art is extracted and re-attached so MKV stores it as an attachment
	pics, err := probe.ExtractAttachedPics(inputPath, plan.CoverArt, runDir)
	if err != nil {
		result.Error = err
		e.markFailed(inputPath, tempOutput)
		return result, result.Error
	}

	// Two-pass encode:
	//   Pass 1 – encode only the video stream. Keeping audio/subtitle tracks out
	//            of this command prevents ffmpeg's interleaving buffer from
	//            accumulating GBs of audio/PGS data while the slow video encoder
	//            works, which is the root cause of the OOM on remux files.
	//   Pass 2 – stream-copy the finished video together with every other stream
	//            from the original. No encoder runs, so this completes in seconds.
	videoOnlyPath := filepath.Join(runDir, "video_only.mkv")

	stepOneCmd := BuildVideoOnlyCommand(inputPath, videoOnlyPath, metadata, plan, profile, e.opts.UseAV1, e.opts.Target)
	e.log.Debug("  ffmpeg pass 1 (video encode): %s", stepOneCmd.String())

	startTime := time.Now()
	totalFrames := metadata.GetTotalFrames()
	if err := e.runFFmpeg(stepOneCmd, totalFrames); err != nil {
		os.Remove(videoOnlyPath)
		result.Error = fmt.Errorf("ffmpeg pass 1 (video encode) failed: %w", err)
		result.Status = "failed"
		e.markFailed(inputPath, tempOutput)
		return result, result.Error
	}

	stepTwoCmd := BuildMergeCommand(inputPath, videoOnlyPath, tempOutput, metadata, plan, pics)
	e.log.Debug("  ffmpeg pass 2 (merge): %s", stepTwoCmd.String())
	e.log.Info("  Merging streams...")

	if err := e.runFFmpeg(stepTwoCmd, 0); err != nil {
		os.Remove(videoOnlyPath)
		result.Error = fmt.Errorf("ffmpeg pass 2 (merge) failed: %w", err)
		result.Status = "failed"
		e.markFailed(inputPath, tempOutput)
		return result, result.Error
	}
	os.Remove(videoOnlyPath)

	result.EncodeDuration = time.Since(startTime)

	e.log.ProgressDone() // Clear progress line

	// ffmpeg treats decode errors as non-fatal, so a corrupt source can yield a
	// truncated output with exit code 0. Never accept an output whose duration
	// doesn't match the source.
	if err := verifyOutput(tempOutput, duration, plan.expectedStreams()); err != nil {
		result.Error = fmt.Errorf("output verification failed: %w", err)
		result.Status = "failed"
		e.markFailed(inputPath, tempOutput)
		return result, result.Error
	}

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
	if result.Saved > 0 && result.SavedPercent >= e.opts.MinSavings {
		// Get original file timestamps before doing anything
		var originalModTime time.Time
		if e.opts.PreserveTimestamps {
			if info, err := os.Stat(inputPath); err == nil {
				originalModTime = info.ModTime()
			}
		}

		finalPath := finalOutputPath(inputPath, strings.ToLower(codecName), e.opts.Delete)

		// Copy temp file to final location (can't use Rename across filesystems).
		// The copy is atomic, so the original is never left half-overwritten
		// when finalPath == inputPath. Success metadata was set during the encode.
		if err := probe.CopyFileAtomic(tempOutput, finalPath); err != nil {
			result.Error = fmt.Errorf("failed to copy encoded file: %w", err)
			os.Remove(tempOutput)
			return result, result.Error
		}
		os.Remove(tempOutput)
		result.OutputPath = finalPath

		if e.opts.PreserveTimestamps && !originalModTime.IsZero() {
			if err := os.Chtimes(finalPath, originalModTime, originalModTime); err != nil {
				e.log.Warn("  Failed to preserve timestamp: %v", err)
			}
		}

		if e.opts.Delete {
			// Encoded file is smaller - replace original
			// Only now remove the original file (if different from finalPath)
			if inputPath != finalPath {
				if err := os.Remove(inputPath); err != nil {
					e.log.Warn("  Failed to remove original file: %v", err)
				}
			}
			probe.RemoveSidecar(inputPath) // Clear any stale failed/discarded status

			if finalPath != filepath.Join(filepath.Dir(inputPath), nameWithoutExt+".mkv") {
				e.log.Warn("  %s.mkv already exists, saved as %s instead", nameWithoutExt, filepath.Base(finalPath))
			}
			e.log.Info("  ✓ Replaced original (smaller)")
			result.Status = "replaced"
		} else {
			// Keep the original; record success on it so the next run skips it
			e.recordStatus(inputPath, probe.StatusSuccess)
			e.log.Info("  ✓ Encoded to %s (use --delete to replace original)", finalPath)
			result.Status = "encoded"
		}
	} else {
		// Encode is larger, or saves less than the minimum - discard
		if result.Saved > 0 {
			e.log.Info("  ✗ Discarded encode (saving below the %.1f%% minimum)", e.opts.MinSavings)
		} else {
			e.log.Info("  ✗ Discarded encode (larger than original)")
		}
		result.Status = "discarded"

		// Record discarded status so we don't try again with this codec
		e.recordStatus(inputPath, e.newDiscardStatus(goencStatus))

		// Now remove the temp output
		os.Remove(tempOutput)
	}

	return result, nil
}

// tempRunDir returns this run's private temp directory, creating it on first use.
// A per-run directory stops concurrent runs (or same-named files) from clobbering
// each other's temp output, and lets Close remove only what this run created.
func (e *Encoder) tempRunDir() (string, error) {
	if e.runDir != "" {
		return e.runDir, nil
	}
	if err := os.MkdirAll(e.opts.TempDir, 0755); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(e.opts.TempDir, "run-*")
	if err != nil {
		return "", err
	}
	e.runDir = dir
	return dir, nil
}

// finalOutputPath picks where the encoded file goes, never overwriting an
// unrelated file. inputPath must be clean. When replacing, it prefers
// <name>.mkv (which may be the input itself). Otherwise, or if that name
// belongs to a different file, it uses <name>.<codec>.mkv, adding a counter
// if that is taken too.
func finalOutputPath(inputPath, codecTag string, replace bool) string {
	dir := filepath.Dir(inputPath)
	base := filepath.Base(inputPath)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	exists := func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}

	if replace {
		p := filepath.Join(dir, name+".mkv")
		if p == inputPath || !exists(p) {
			return p
		}
	}
	p := filepath.Join(dir, name+"."+codecTag+".mkv")
	for i := 1; exists(p); i++ {
		p = filepath.Join(dir, fmt.Sprintf("%s.%s.%d.mkv", name, codecTag, i))
	}
	return p
}

// is3D reports whether a file is 3D, from a "[3D]" filename marker or the
// video's stereoscopic metadata
func is3D(inputPath string, metadata *probe.Metadata) bool {
	return strings.Contains(strings.ToUpper(filepath.Base(inputPath)), "[3D]") || metadata.Is3D()
}

// discardedStatus returns the discard status for the codec in use
func (e *Encoder) discardedStatus() probe.GoencStatus {
	if e.opts.UseAV1 {
		return probe.StatusDiscardedAV1
	}
	return probe.StatusDiscarded
}

// newDiscardStatus returns the status to record when discarding with the current
// codec, keeping an existing discard for the other codec so neither is retried
func (e *Encoder) newDiscardStatus(prev probe.GoencStatus) probe.GoencStatus {
	other := probe.StatusDiscardedAV1
	if e.opts.UseAV1 {
		other = probe.StatusDiscarded
	}
	if probe.HasStatus(prev, other) {
		return probe.StatusDiscarded + "+" + probe.StatusDiscardedAV1
	}
	return e.discardedStatus()
}

// markFailed records a failed status for the original and removes the temp output
func (e *Encoder) markFailed(inputPath, tempOutput string) {
	os.Remove(tempOutput)
	e.recordStatus(inputPath, probe.StatusFailed)
}

// recordStatus embeds the status in the original's metadata. If that can't be
// done safely (unsupported container, verification failure, etc.) the original
// is left untouched and the status goes in a sidecar file instead.
func (e *Encoder) recordStatus(inputPath string, status probe.GoencStatus) {
	e.log.Debug("  Writing %s status to metadata...", status)
	err := probe.WriteStatusTag(inputPath, status, e.opts.PreserveTimestamps)
	if err == nil {
		probe.RemoveSidecar(inputPath) // Clear any stale sidecar status
		return
	}
	e.log.Warn("  Could not embed status in file, using sidecar instead: %v", err)
	if err := probe.WriteSidecar(inputPath, status); err != nil {
		e.log.Warn("  Failed to write status: %v", err)
	}
}

// verifyOutput checks that the encoded output has every planned stream and is
// as long as the source. Duration tolerance is the larger of 2 seconds or 1%.
func verifyOutput(outputPath string, sourceDuration float64, expectedStreams int) error {
	if sourceDuration <= 0 {
		return fmt.Errorf("source duration unknown, cannot verify output")
	}
	outMeta, err := probe.GetMetadata(outputPath)
	if err != nil {
		return fmt.Errorf("failed to probe output: %w", err)
	}
	if len(outMeta.Streams) != expectedStreams {
		return fmt.Errorf("stream count mismatch: expected %d, output has %d", expectedStreams, len(outMeta.Streams))
	}
	outDuration, err := outMeta.GetDuration()
	if err != nil {
		return fmt.Errorf("failed to read output duration: %w", err)
	}
	tolerance := math.Max(2.0, sourceDuration*0.01)
	if math.Abs(outDuration-sourceDuration) > tolerance {
		return fmt.Errorf("duration mismatch: source %.1fs, output %.1fs", sourceDuration, outDuration)
	}
	return nil
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

// Close removes this run's temp directory
func (e *Encoder) Close() error {
	if e.runDir != "" {
		return os.RemoveAll(e.runDir)
	}
	return nil
}
