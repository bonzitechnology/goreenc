package encoder

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/kronicd/goreenc/internal/probe"
)

// mkvSubtitleCodecs are subtitle codecs Matroska can hold as-is.
// ASS/SSA are copied rather than converted so their styling survives.
var mkvSubtitleCodecs = map[string]bool{
	"subrip":            true,
	"ass":               true,
	"ssa":               true,
	"webvtt":            true,
	"hdmv_pgs_subtitle": true,
	"dvd_subtitle":      true,
	"dvb_subtitle":      true,
}

// subtitlePlan describes what to do with one input subtitle stream
type subtitlePlan struct {
	probe.SubtitleStream
	Convert bool // true = convert to SRT, false = copy
}

// planSubtitles decides, per subtitle stream, whether to copy it, convert it to
// SRT, or drop it. Streams that are neither MKV-compatible nor convertible
// (e.g. eia_608, dvb_teletext) would make ffmpeg fail, so they are dropped.
func planSubtitles(metadata *probe.Metadata) (kept []subtitlePlan, dropped []probe.SubtitleStream) {
	for _, sub := range metadata.GetSubtitleStreams() {
		switch {
		case mkvSubtitleCodecs[strings.ToLower(sub.CodecName)]:
			kept = append(kept, subtitlePlan{SubtitleStream: sub})
		case sub.IsTextBased:
			kept = append(kept, subtitlePlan{SubtitleStream: sub, Convert: true})
		default:
			dropped = append(dropped, sub)
		}
	}
	return kept, dropped
}

// BuildFFmpegCommand builds the ffmpeg command for encoding
func BuildFFmpegCommand(inputPath, outputPath string, metadata *probe.Metadata, profile Profile, useAV1, keep4K bool) *exec.Cmd {
	args := []string{
		"-hide_banner",
		"-progress", "pipe:2", // Output progress to stderr in parseable format
		"-i", inputPath,
		// Map streams
		"-map", "0:V?", // Map real video streams (capital V excludes cover art, which would otherwise be re-encoded as a 1-frame video track)
		"-map", "0:a?", // Map all audio streams
	}

	// Subtitles are mapped individually so unsupported ones can be left out
	subtitles, _ := planSubtitles(metadata)
	for _, sub := range subtitles {
		args = append(args, "-map", fmt.Sprintf("0:%d", sub.StreamIndex))
	}

	args = append(args,
		"-map", "0:t?", // Map attachments (fonts, cover art, etc.) - MKV only
		"-map_metadata", "0", // Copy all metadata tags
		"-map_chapters", "0", // Copy chapter markers
		"-metadata", "comment="+probe.StatusComment(probe.StatusSuccess), // Mark output as processed (only kept if the encode is accepted)
		"-c", "copy", // Copy all streams by default; dispositions (default/forced flags) carry over from the source
		// Set the video disposition explicitly. When none is given, ffmpeg marks the
		// first stream of each type as default if no stream of that type is, which
		// turns on the first subtitle track on playback.
		"-disposition:v:0", probe.DispositionArg(metadata.GetVideoDisposition()),
	)

	// Output subtitle indexes follow the order they were mapped in
	for i, sub := range subtitles {
		if sub.Convert {
			args = append(args, fmt.Sprintf("-c:s:%d", i), "srt")
		}
	}

	args = append(args, videoEncodeArgs(metadata, profile, useAV1, keep4K)...)

	// Output file
	args = append(args, "-y", outputPath) // -y to overwrite

	return exec.Command("ffmpeg", args...)
}

// videoEncodeArgs returns the video encoding options (codec, pixel format,
// colour, scaling, quality). Shared by the full encode and sample encodes so
// estimates use exactly the same settings.
func videoEncodeArgs(metadata *probe.Metadata, profile Profile, useAV1, keep4K bool) []string {
	var args []string

	// Set video codec
	if useAV1 {
		args = append(args, "-c:v", "libsvtav1")
	} else {
		args = append(args, "-c:v", "libx265") // Re-encode video to HEVC
	}

	// Force 4:2:0 at the source's bit depth: 8-bit sources become HEVC Main (the
	// most widely playable profile), 10-bit+ sources and HDR stay 10-bit (Main10)
	// to avoid banding. Without this, 4:2:2 and 4:4:4 sources produce HEVC Rext
	// profile, which most hardware decoders can't play.
	pixFmt := "yuv420p"
	if metadata.IsHighBitDepth() {
		pixFmt = "yuv420p10le"
	}
	args = append(args, "-pix_fmt", pixFmt)

	// Carry the source's color signalling across explicitly. Without it, HDR
	// (PQ/HLG) or BT.2020 content can be flagged as SDR and play back washed out.
	color := metadata.GetColorInfo()
	if color.Primaries != "" {
		args = append(args, "-color_primaries", color.Primaries)
	}
	if color.Transfer != "" {
		args = append(args, "-color_trc", color.Transfer)
	}
	if color.Space != "" {
		args = append(args, "-colorspace", color.Space)
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

	return args
}

// BuildSampleCommand builds an ffmpeg command that encodes only the main video
// stream between start and start+length seconds, for savings estimates
func BuildSampleCommand(inputPath, outputPath string, metadata *probe.Metadata, profile Profile, useAV1, keep4K bool, start, length float64) *exec.Cmd {
	args := []string{
		"-v", "error",
		"-ss", fmt.Sprintf("%.3f", start),
		"-t", fmt.Sprintf("%.3f", length),
		"-i", inputPath,
		"-map", fmt.Sprintf("0:%d", metadata.PrimaryVideoIndex()),
		"-an", "-sn", "-dn",
	}
	args = append(args, videoEncodeArgs(metadata, profile, useAV1, keep4K)...)
	args = append(args, "-y", outputPath)
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
