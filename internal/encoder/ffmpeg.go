package encoder

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/kronicd/goreenc/internal/probe"
)

// BuildVideoOnlyCommand builds the ffmpeg command for step 1 of a two-pass
// encode: re-encode all video streams to videoPath, with no audio or subtitle
// streams. This keeps ffmpeg's interleaving buffer empty, which prevents the
// OOM that occurs when a slow encoder (libx265/libsvtav1) is run alongside
// many stream-copied tracks.
func BuildVideoOnlyCommand(inputPath, videoPath string, metadata *probe.Metadata, plan *streamPlan, profile Profile, useAV1 bool, target Target) *exec.Cmd {
	args := []string{
		"-hide_banner",
		"-progress", "pipe:2",
		"-i", inputPath,
	}

	// Map all video streams being encoded, in plan order
	for _, ps := range plan.Mapped {
		if ps.Action == actionEncode {
			args = append(args, "-map", fmt.Sprintf("0:%d", ps.Index))
		}
	}
	args = append(args, "-an", "-sn", "-dn") // no audio, subtitles, or data

	// Per-stream encode options; output indices are 0, 1, … for each encoded stream
	outIdx := 0
	for _, ps := range plan.Mapped {
		if ps.Action == actionEncode {
			args = append(args, videoEncodeArgs(metadata, profile, useAV1, target, strconv.Itoa(outIdx))...)
			outIdx++
		}
	}

	args = append(args, "-y", videoPath)
	return exec.Command("ffmpeg", args...)
}

// BuildMergeCommand builds the ffmpeg command for step 2 of a two-pass encode:
// merge the re-encoded video (from videoPath) with every non-video stream from
// the original input. Because this step is a pure stream copy with no slow
// encoder running, ffmpeg's interleaving buffer stays tiny.
//
// Input 0 = videoPath (encoded video only), input 1 = inputPath for audio,
// attachments and data, input 2 = inputPath again for subtitles only.
//
// Subtitles must not share an input with audio. ffmpeg's scheduler keeps
// reading whichever input feeds the output stream that is furthest behind; a
// sparse subtitle track lags constantly, so with audio and subtitles on one
// input it reads that input almost exclusively and starves the video input.
// The muxer then writes tens of minutes of audio before the video resumes,
// giving a file that starts with no picture and loses audio after a seek.
// Raising -max_interleave_delta doesn't help, and setting it to 0 buffers GBs.
func BuildMergeCommand(inputPath, videoPath, outputPath string, metadata *probe.Metadata, plan *streamPlan, pics []probe.AttachedPic) *exec.Cmd {
	args := []string{
		"-hide_banner",
		"-progress", "pipe:2",
		"-i", videoPath,
		"-i", inputPath,
		"-i", inputPath,
	}

	// All streams from the encoded video file (input 0); videoPath contains only
	// the re-encoded video tracks, so -map 0 picks up every one of them.
	args = append(args, "-map", "0")

	// All non-video streams come from the original, preserving order:
	// subtitles from input 2, everything else from input 1
	attachments := 0
	for _, ps := range plan.Mapped {
		if ps.Action == actionEncode {
			continue
		}
		input := 1
		if ps.CodecType == "subtitle" {
			input = 2
		}
		args = append(args, "-map", fmt.Sprintf("%d:%d", input, ps.Index))
		if ps.CodecType == "attachment" {
			attachments++
		}
	}

	args = append(args,
		"-map_metadata", "1", // metadata tags from original
		"-map_chapters", "1", // chapter markers from original
		"-metadata", "comment="+probe.StatusComment(probe.StatusSuccess),
		"-c", "copy", // everything is a stream copy; the video is already encoded
	)

	// Set disposition for each encoded video stream; output indices 0, 1, …
	outIdx := 0
	for _, ps := range plan.Mapped {
		if ps.Action == actionEncode {
			args = append(args, fmt.Sprintf("-disposition:%d", outIdx), probe.DispositionArg(ps.Disposition))
			outIdx++
		}
	}

	// SRT conversions need their codec flag on the non-video output streams.
	// Those start at output index <videoCount> (right after all encoded videos).
	videoCount := outIdx
	outIdx = videoCount
	for _, ps := range plan.Mapped {
		if ps.Action == actionEncode {
			continue
		}
		if ps.Action == actionToSRT {
			args = append(args, fmt.Sprintf("-c:%d", outIdx), "srt")
		}
		outIdx++
	}

	args = append(args, probe.AttachArgs(pics, attachments)...)
	args = append(args, "-y", outputPath)
	return exec.Command("ffmpeg", args...)
}

// videoEncodeArgs returns the video encoding options (codec, pixel format,
// colour, scaling, quality). Shared by the full encode and sample encodes so
// estimates use exactly the same settings.
// spec is the output stream specifier the options apply to (e.g. "0").
func videoEncodeArgs(metadata *probe.Metadata, profile Profile, useAV1 bool, target Target, spec string) []string {
	var args []string
	opt := func(name string) string { return name + ":" + spec }

	// Set video codec
	if useAV1 {
		args = append(args, opt("-c"), "libsvtav1")
	} else {
		args = append(args, opt("-c"), "libx265") // Re-encode video to HEVC
	}

	// Force 4:2:0 at the source's bit depth: 8-bit sources become HEVC Main (the
	// most widely playable profile), 10-bit+ sources and HDR stay 10-bit (Main10)
	// to avoid banding. Without this, 4:2:2 and 4:4:4 sources produce HEVC Rext
	// profile, which most hardware decoders can't play.
	pixFmt := "yuv420p"
	if metadata.IsHighBitDepth() {
		pixFmt = "yuv420p10le"
	}
	args = append(args, opt("-pix_fmt"), pixFmt)

	// Carry the source's color signalling across explicitly. Without it, HDR
	// (PQ/HLG) or BT.2020 content can be flagged as SDR and play back washed out.
	color := metadata.GetColorInfo()
	if color.Primaries != "" {
		args = append(args, opt("-color_primaries"), color.Primaries)
	}
	if color.Transfer != "" {
		args = append(args, opt("-color_trc"), color.Transfer)
	}
	if color.Space != "" {
		args = append(args, opt("-colorspace"), color.Space)
	}

	// Downscale sources larger than the target resolution
	if width, height := metadata.GetResolution(); target.NeedsScale(width, height) {
		args = append(args, opt("-filter"), target.ScaleFilter())
	}

	// Add CRF quality
	args = append(args, opt("-crf"), fmt.Sprintf("%d", profile.CRF))

	// Add preset
	args = append(args, opt("-preset"), profile.Preset)

	// Add codec-specific params
	if useAV1 {
		// AV1 encoder params for svt-av1
		params := append([]string{"tune=0"}, target.svtAV1Params()...)
		args = append(args, opt("-svtav1-params"), strings.Join(params, ":"))
	} else {
		// x265 expects all params joined with ':' in a single flag
		params := append(append([]string{}, profile.X265Params...), target.x265Params()...)
		if len(params) > 0 {
			args = append(args, opt("-x265-params"), strings.Join(params, ":"))
		}
	}

	return args
}

// BuildSampleCommand builds an ffmpeg command that encodes only the main video
// stream between start and start+length seconds, for savings estimates
func BuildSampleCommand(inputPath, outputPath string, metadata *probe.Metadata, profile Profile, useAV1 bool, target Target, start, length float64) *exec.Cmd {
	args := []string{
		"-v", "error",
		"-ss", fmt.Sprintf("%.3f", start),
		"-t", fmt.Sprintf("%.3f", length),
		"-i", inputPath,
		"-map", fmt.Sprintf("0:%d", metadata.PrimaryVideoIndex()),
		"-an", "-sn", "-dn",
	}
	args = append(args, videoEncodeArgs(metadata, profile, useAV1, target, "0")...)
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
