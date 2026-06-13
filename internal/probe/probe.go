package probe

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Metadata represents the output from ffprobe
type Metadata struct {
	Format  Format   `json:"format"`
	Streams []Stream `json:"streams"`
}

// Format contains container format information
type Format struct {
	Filename   string            `json:"filename"`
	Size       string            `json:"size"`
	Duration   string            `json:"duration"`
	FormatName string            `json:"format_name"`
	Tags       map[string]string `json:"tags"`
}

// Stream represents a single stream (video, audio, subtitle, etc.)
type Stream struct {
	Index              int               `json:"index"`
	CodecName          string            `json:"codec_name"`
	CodecLongName      string            `json:"codec_long_name"`
	CodecType          string            `json:"codec_type"`
	Width              int               `json:"width"`
	Height             int               `json:"height"`
	PixFmt             string            `json:"pix_fmt"`
	Profile            string            `json:"profile"`
	Channels           int               `json:"channels"`
	ChannelLayout      string            `json:"channel_layout"`
	SampleRate         string            `json:"sample_rate"`
	BitRate            string            `json:"bit_rate"`
	BitsPerRawSample   string            `json:"bits_per_raw_sample"`
	AvgFrameRate       string            `json:"avg_frame_rate"`
	Duration           string            `json:"duration"`
	NbFrames           string            `json:"nb_frames"`
	Tags               map[string]string `json:"tags"`
	Disposition        map[string]int    `json:"disposition"`
}

// GetMetadata runs ffprobe on a file and returns parsed metadata
func GetMetadata(filePath string) (*Metadata, error) {
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		filePath,
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	var metadata Metadata
	if err := json.Unmarshal(output, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	return &metadata, nil
}

// GetResolution returns the resolution of the first video stream
func (m *Metadata) GetResolution() (width, height int) {
	for _, stream := range m.Streams {
		if stream.CodecType == "video" {
			return stream.Width, stream.Height
		}
	}
	return 0, 0
}

// GetVideoCodec returns the codec name of the first video stream
func (m *Metadata) GetVideoCodec() string {
	for _, stream := range m.Streams {
		if stream.CodecType == "video" {
			return stream.CodecName
		}
	}
	return ""
}

// GetFileSize returns the file size in bytes
func (m *Metadata) GetFileSize() (int64, error) {
	if m.Format.Size == "" {
		return 0, fmt.Errorf("size not available in metadata")
	}
	return strconv.ParseInt(m.Format.Size, 10, 64)
}

// GetDuration returns the duration in seconds
func (m *Metadata) GetDuration() (float64, error) {
	if m.Format.Duration == "" {
		return 0, fmt.Errorf("duration not available in metadata")
	}
	return strconv.ParseFloat(m.Format.Duration, 64)
}

// GetTotalFrames returns the total number of frames in the video
func (m *Metadata) GetTotalFrames() int {
	for _, stream := range m.Streams {
		if stream.CodecType == "video" {
			if stream.NbFrames != "" {
				frames, _ := strconv.Atoi(stream.NbFrames)
				return frames
			}
		}
	}
	return 0
}

// IsHEVC checks if the video is already encoded with HEVC
func (m *Metadata) IsHEVC() bool {
	codec := m.GetVideoCodec()
	return codec == "hevc" || codec == "h265"
}

// IsAV1 checks if the video is already encoded with AV1
func (m *Metadata) IsAV1() bool {
	codec := m.GetVideoCodec()
	return codec == "av1"
}

// GetStreamCounts returns counts of each stream type
func (m *Metadata) GetStreamCounts() (video, audio, subtitle, other int) {
	for _, stream := range m.Streams {
		switch stream.CodecType {
		case "video":
			video++
		case "audio":
			audio++
		case "subtitle":
			subtitle++
		default:
			other++
		}
	}
	return
}

// GoencStatus represents the processing status stored in metadata
type GoencStatus string

const (
	StatusSuccess   GoencStatus = "success"   // Successfully encoded and replaced
	StatusDiscarded GoencStatus = "discarded" // Encoded but larger, discarded
	StatusFailed    GoencStatus = "failed"    // Encoding failed with error
	StatusNone      GoencStatus = ""          // Never processed by goenc
)

// GetGoencStatus checks if the file has been processed by goenc before
func (m *Metadata) GetGoencStatus() GoencStatus {
	// Helper to get tag case-insensitively
	getTag := func(key string) (string, bool) {
		for k, v := range m.Format.Tags {
			if strings.EqualFold(k, key) {
				return v, true
			}
		}
		return "", false
	}

	// Check format-level tags first (for MKV/other formats)
	if status, ok := getTag("goenc_status"); ok {
		return GoencStatus(status)
	}

	// Check comment tag (for MP4 compatibility)
	// Format: "goenc:status:timestamp"
	if comment, ok := getTag("comment"); ok {
		if len(comment) > 6 && strings.ToLower(comment[:6]) == "goenc:" {
			parts := splitN(comment, ":", 3)
			if len(parts) >= 2 {
				return GoencStatus(parts[1])
			}
		}
	}

	return StatusNone
}

// splitN splits a string by separator, max n parts
func splitN(s, sep string, n int) []string {
	var result []string
	start := 0
	for i := 0; i < len(s) && len(result) < n-1; i++ {
		if s[i] == sep[0] {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

// GetGoencTimestamp returns when the file was processed by goenc
func (m *Metadata) GetGoencTimestamp() string {
	// Helper to get tag case-insensitively
	getTag := func(key string) (string, bool) {
		for k, v := range m.Format.Tags {
			if strings.EqualFold(k, key) {
				return v, true
			}
		}
		return "", false
	}

	// Check format-level tags first (for MKV/other formats)
	if timestamp, ok := getTag("goenc_timestamp"); ok {
		return timestamp
	}

	// Check comment tag (for MP4 compatibility)
	// Format: "goenc:status:timestamp"
	if comment, ok := getTag("comment"); ok {
		if len(comment) > 6 && strings.ToLower(comment[:6]) == "goenc:" {
			parts := splitN(comment, ":", 3)
			if len(parts) >= 3 {
				return parts[2]
			}
		}
	}

	return ""
}
