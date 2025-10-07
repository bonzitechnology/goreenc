package encoder

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kronicd/goreenc/internal/probe"
)

// BuildFFmpegCommand builds the ffmpeg command for encoding
func BuildFFmpegCommand(inputPath, outputPath string, metadata *probe.Metadata, profile Profile, useAV1, keep4K bool) *exec.Cmd {
	args := []string{
		"-hide_banner",
		"-progress", "pipe:2", // Output progress to stderr in parseable format
		"-i", inputPath,
		// Map only video, audio, and subtitle streams (MKV doesn't support other stream types)
		"-map", "0:v?", // Map all video streams
		"-map", "0:a?", // Map all audio streams
		"-map", "0:s?", // Map all subtitle streams
		"-c", "copy",   // Copy all streams by default
		"-c:s", "srt",  // Convert subtitles to SRT format (widely supported)
	}

	// Set video codec
	if useAV1 {
		args = append(args, "-c:v", "libsvtav1")
	} else {
		args = append(args, "-c:v", "libx265") // Re-encode video to HEVC
	}

	// Check if we need to downscale (unless --4k flag is set)
	_, height := metadata.GetResolution()
	if !keep4K && height > 1080 {
		// Downscale to 1080p, preserving aspect ratio
		// -2 means maintain aspect ratio (make divisible by 2)
		args = append(args, "-vf", "scale=-2:1080")
	}

	// Add CRF quality
	args = append(args, "-crf", fmt.Sprintf("%d", profile.CRF))

	// Add preset
	args = append(args, "-preset", profile.Preset)

	// Add codec-specific params
	if useAV1 {
		// AV1 encoder params for svt-av1
		args = append(args, "-svtav1-params", "tune=0")
	} else {
		// Add x265 params
		for _, param := range profile.X265Params {
			args = append(args, "-x265-params", param)
		}
	}

	// Add normalize-level 0 equivalent (no audio normalization)
	// This is implicitly handled by using -c:a copy

	// Add -F 1 equivalent (faststart for mp4)
	ext := strings.ToLower(filepath.Ext(outputPath))
	if ext == ".mp4" || ext == ".m4v" {
		args = append(args, "-movflags", "+faststart")
	}

	// Output file
	args = append(args, "-y", outputPath) // -y to overwrite

	return exec.Command("ffmpeg", args...)
}

// ProgressInfo holds parsed progress information
type ProgressInfo struct {
	Frame    int
	FPS      float64
	Bitrate  string
	Size     string
	Time     string
	Speed    string
}

// progressData accumulates progress key-value pairs
type progressData struct {
	frame int
	fps   float64
	size  string
	time  string
	speed string
}

var currentProgress = &progressData{}

// ParseProgress parses ffmpeg progress output
// With -progress pipe:2, ffmpeg outputs key=value pairs, one per line
func ParseProgress(line string) *ProgressInfo {
	// Progress format with -progress pipe:2:
	// frame=123
	// fps=18.4
	// total_size=1234567
	// out_time_us=1234567
	// speed=1.2x
	// progress=continue (or end)

	if !strings.Contains(line, "=") {
		return nil
	}

	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return nil
	}

	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])

	// Update current progress data
	switch key {
	case "frame":
		fmt.Sscanf(value, "%d", &currentProgress.frame)
	case "fps":
		fmt.Sscanf(value, "%f", &currentProgress.fps)
	case "total_size":
		// Convert bytes to human-readable
		var sizeBytes int64
		fmt.Sscanf(value, "%d", &sizeBytes)
		currentProgress.size = fmt.Sprintf("%.1fMB", float64(sizeBytes)/(1024*1024))
	case "out_time_us":
		// Convert microseconds to HH:MM:SS
		var timeUs int64
		fmt.Sscanf(value, "%d", &timeUs)
		seconds := timeUs / 1000000
		h := seconds / 3600
		m := (seconds % 3600) / 60
		s := seconds % 60
		currentProgress.time = fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	case "speed":
		currentProgress.speed = value
	case "progress":
		// "continue" or "end" - return current progress snapshot
		if value == "continue" || value == "end" {
			return &ProgressInfo{
				Frame:   currentProgress.frame,
				FPS:     currentProgress.fps,
				Size:    currentProgress.size,
				Time:    currentProgress.time,
				Speed:   currentProgress.speed,
			}
		}
	}

	return nil
}
