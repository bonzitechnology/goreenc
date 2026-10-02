package encoder

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/kronicd/goreenc/internal/probe"
)

// BuildFFmpegCommand builds the ffmpeg command for encoding
func BuildFFmpegCommand(inputPath, outputPath string, metadata *probe.Metadata, profile Profile, useAV1, keep4K bool) *exec.Cmd {
	args := []string{
		"-hide_banner",
		"-progress", "pipe:2", // Output progress to stderr in parseable format
		"-i", inputPath,
		// Map all streams
		"-map", "0:v?",       // Map all video streams
		"-map", "0:a?",       // Map all audio streams
		"-map", "0:s?",       // Map all subtitle streams
		"-map", "0:t?",       // Map attachments (fonts, cover art, etc.) - MKV only
		"-map_metadata", "0", // Copy all metadata tags
		"-map_chapters", "0", // Copy chapter markers
		"-c", "copy",         // Copy all streams by default
		"-disposition:a:0", "default", // Keep default audio track disposition
		"-disposition:s:0", "default", // Keep default subtitle track disposition
	}

	// Subtitle codec selection: convert text-based subs to SRT, copy image-based ones.
	// Image-based formats (PGS, VOBSUB, DVB) cannot be decoded to SRT by ffmpeg and
	// will cause an error if you attempt conversion.
	subtitleStreams := metadata.GetSubtitleStreams()
	if len(subtitleStreams) == 0 {
		// No subtitle streams — set a safe default anyway
		args = append(args, "-c:s", "copy")
	} else {
		// Check if all streams share the same codec decision to keep args compact
		allText := true
		allImage := true
		for _, sub := range subtitleStreams {
			if sub.IsTextBased {
				allImage = false
			} else {
				allText = false
			}
		}

		switch {
		case allText:
			// All text-based: convert everything to SRT
			args = append(args, "-c:s", "srt")
		case allImage:
			// All image-based: copy everything
			args = append(args, "-c:s", "copy")
		default:
			// Mixed: set per-stream codec
			args = append(args, "-c:s", "copy") // safe default for any unaddressed streams
			for _, sub := range subtitleStreams {
				if sub.IsTextBased {
					args = append(args,
						fmt.Sprintf("-c:s:%d", sub.SubtitleIndex), "srt",
					)
				}
				// Image-based streams fall through to the "copy" default above
			}
		}
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
		// x265 expects all params joined with ':' in a single flag
		if params := profile.GetX265ParamsString(); params != "" {
			args = append(args, "-x265-params", params)
		}
	}

	// Output file
	args = append(args, "-y", outputPath) // -y to overwrite

	return exec.Command("ffmpeg", args...)
}

// ProgressInfo holds parsed progress information
type ProgressInfo struct {
	Frame   int
	FPS     float64
	Bitrate string
	Size    string
	Time    string
	Speed   string
}

// progressData accumulates progress key-value pairs within a single parse session
type progressData struct {
	frame int
	fps   float64
	size  string
	time  string
	speed string
}

// ParseProgress parses ffmpeg progress output.
// With -progress pipe:2, ffmpeg outputs key=value pairs, one per line,
// followed by a "progress=continue" or "progress=end" marker.
// Call this with a stateful *progressData that you own per-encode session.
func ParseProgress(line string, current *progressData) *ProgressInfo {
	if !strings.Contains(line, "=") {
		return nil
	}

	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return nil
	}

	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])

	switch key {
	case "frame":
		fmt.Sscanf(value, "%d", &current.frame)
	case "fps":
		fmt.Sscanf(value, "%f", &current.fps)
	case "total_size":
		// Convert bytes to human-readable
		var sizeBytes int64
		fmt.Sscanf(value, "%d", &sizeBytes)
		current.size = fmt.Sprintf("%.1fMB", float64(sizeBytes)/(1024*1024))
	case "out_time_us":
		// Convert microseconds to HH:MM:SS
		var timeUs int64
		fmt.Sscanf(value, "%d", &timeUs)
		seconds := timeUs / 1000000
		h := seconds / 3600
		m := (seconds % 3600) / 60
		s := seconds % 60
		current.time = fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	case "speed":
		current.speed = value
	case "progress":
		// "continue" or "end" — emit a snapshot of the current accumulated state
		if value == "continue" || value == "end" {
			return &ProgressInfo{
				Frame: current.frame,
				FPS:   current.fps,
				Size:  current.size,
				Time:  current.time,
				Speed: current.speed,
			}
		}
	}

	return nil
}
