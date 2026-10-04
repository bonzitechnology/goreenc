package probe

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
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
	ColorPrimaries     string            `json:"color_primaries"`
	ColorTransfer      string            `json:"color_transfer"`
	ColorSpace         string            `json:"color_space"`
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
	SideDataList       []SideData        `json:"side_data_list"`
}

// SideData is a stream side data entry. Only the fields goenc needs are decoded.
type SideData struct {
	Type      string `json:"side_data_type"`
	DVProfile int    `json:"dv_profile"`
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

// IsAttachedPic reports whether a video stream is embedded cover art rather than actual video
func (s *Stream) IsAttachedPic() bool {
	return s.Disposition["attached_pic"] == 1
}

// isPrimaryVideo reports whether a stream is real video (not cover art)
func (s *Stream) isPrimaryVideo() bool {
	return s.CodecType == "video" && !s.IsAttachedPic()
}

// GetResolution returns the resolution of the first video stream
func (m *Metadata) GetResolution() (width, height int) {
	for _, stream := range m.Streams {
		if stream.isPrimaryVideo() {
			return stream.Width, stream.Height
		}
	}
	return 0, 0
}

// GetVideoCodec returns the codec name of the first video stream
func (m *Metadata) GetVideoCodec() string {
	for _, stream := range m.Streams {
		if stream.isPrimaryVideo() {
			return stream.CodecName
		}
	}
	return ""
}

// PrimaryVideoIndex returns the stream index of the first real video stream, or -1
func (m *Metadata) PrimaryVideoIndex() int {
	for _, stream := range m.Streams {
		if stream.isPrimaryVideo() {
			return stream.Index
		}
	}
	return -1
}

// EstimateVideoBytes estimates how many bytes of a file are the main video stream.
// It uses the Matroska NUMBER_OF_BYTES statistics tag when present, otherwise the
// file size minus the audio bitrates. If any audio bitrate is unknown it returns
// fileSize, which overestimates savings: estimates then err towards encoding.
func (m *Metadata) EstimateVideoBytes(fileSize int64) int64 {
	tag := func(tags map[string]string, key string) string {
		for k, v := range tags {
			if strings.EqualFold(k, key) || strings.HasPrefix(strings.ToUpper(k), key+"-") {
				return v
			}
		}
		return ""
	}

	for _, stream := range m.Streams {
		if stream.isPrimaryVideo() {
			if n, err := strconv.ParseInt(tag(stream.Tags, "NUMBER_OF_BYTES"), 10, 64); err == nil && n > 0 && n <= fileSize {
				return n
			}
			break
		}
	}

	duration, err := m.GetDuration()
	if err != nil {
		return fileSize
	}
	var audioBytes float64
	for _, stream := range m.Streams {
		if stream.CodecType != "audio" {
			continue
		}
		bitrate, err := strconv.ParseFloat(stream.BitRate, 64)
		if err != nil {
			bitrate, err = strconv.ParseFloat(tag(stream.Tags, "BPS"), 64)
		}
		if err != nil || bitrate <= 0 {
			return fileSize
		}
		audioBytes += bitrate / 8 * duration
	}
	if video := fileSize - int64(audioBytes); video > 0 {
		return video
	}
	return fileSize
}

// PacketBytes sums the packet sizes of one stream whose timestamps fall in
// [start, end) seconds. end <= 0 means the whole stream.
func PacketBytes(filePath string, streamIndex int, start, end float64) (int64, error) {
	args := []string{"-v", "error", "-select_streams", strconv.Itoa(streamIndex)}
	if end > 0 {
		// Seek near the range (lands on an earlier keyframe); packets outside it are filtered below
		args = append(args, "-read_intervals", fmt.Sprintf("%.3f%%%.3f", start, end))
	}
	args = append(args, "-show_entries", "packet=pts_time,size", "-of", "csv=p=0", filePath)

	output, err := exec.Command("ffprobe", args...).Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe failed: %w", err)
	}

	var total int64
	for _, line := range strings.Split(string(output), "\n") {
		parts := strings.Split(strings.TrimSpace(line), ",")
		if len(parts) < 2 {
			continue
		}
		size, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		if end > 0 {
			pts, err := strconv.ParseFloat(parts[0], 64)
			if err != nil || pts < start || pts >= end {
				continue
			}
		}
		total += size
	}
	return total, nil
}

// GetVideoDisposition returns the disposition flags of the first video stream
func (m *Metadata) GetVideoDisposition() map[string]int {
	for _, stream := range m.Streams {
		if stream.isPrimaryVideo() {
			return stream.Disposition
		}
	}
	return nil
}

// highBitDepthPixFmt matches pixel formats with more than 8 bits per component,
// e.g. yuv420p10le, yuv444p12be, gbrp16le, p010le
var highBitDepthPixFmt = regexp.MustCompile(`(p(9|10|12|14|16)(le|be)?|p0(10|12|16)(le|be)?)$`)

// IsHighBitDepth reports whether the first video stream has more than 8 bits
// per component. HDR (PQ/HLG) always counts as high bit depth.
func (m *Metadata) IsHighBitDepth() bool {
	for _, stream := range m.Streams {
		if !stream.isPrimaryVideo() {
			continue
		}
		if bits, err := strconv.Atoi(stream.BitsPerRawSample); err == nil && bits > 8 {
			return true
		}
		switch stream.ColorTransfer {
		case "smpte2084", "arib-std-b67":
			return true
		}
		return highBitDepthPixFmt.MatchString(stream.PixFmt)
	}
	return false
}

// ColorInfo is the color signalling of a video stream. Empty fields are unspecified.
type ColorInfo struct {
	Primaries string
	Transfer  string
	Space     string
}

// GetColorInfo returns the color signalling of the first video stream
func (m *Metadata) GetColorInfo() ColorInfo {
	known := func(v string) string {
		switch v {
		case "unknown", "unspecified", "reserved":
			return ""
		}
		return v
	}
	for _, stream := range m.Streams {
		if stream.isPrimaryVideo() {
			return ColorInfo{
				Primaries: known(stream.ColorPrimaries),
				Transfer:  known(stream.ColorTransfer),
				Space:     known(stream.ColorSpace),
			}
		}
	}
	return ColorInfo{}
}

// GetDolbyVisionProfile returns the Dolby Vision profile of the first video
// stream, or 0 if it has no Dolby Vision configuration
func (m *Metadata) GetDolbyVisionProfile() int {
	for _, stream := range m.Streams {
		if !stream.isPrimaryVideo() {
			continue
		}
		for _, sd := range stream.SideDataList {
			if strings.HasPrefix(sd.Type, "DOVI configuration") {
				return sd.DVProfile
			}
		}
		return 0
	}
	return 0
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
		if stream.isPrimaryVideo() {
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

// subtitleTextCodecs is the set of subtitle codecs that are text-based and
// can be safely converted to SRT without data loss.
var subtitleTextCodecs = map[string]bool{
	"subrip":   true, // SRT (already SRT)
	"ass":      true, // Advanced SubStation Alpha
	"ssa":      true, // SubStation Alpha
	"webvtt":   true, // WebVTT
	"mov_text": true, // iTunes / MP4 text
	"microdvd": true, // MicroDVD
	"sami":     true, // SAMI
	"realtext": true, // RealText
	"text":     true, // Raw text
}

// SubtitleStream holds per-stream subtitle info used for codec selection.
type SubtitleStream struct {
	// StreamIndex is the overall stream index in the file (as reported by ffprobe).
	StreamIndex int
	// SubtitleIndex is the 0-based index among subtitle streams only (for -c:s:<n> targeting).
	SubtitleIndex int
	// CodecName is the ffprobe codec name (e.g. "hdmv_pgs_subtitle", "subrip").
	CodecName string
	// IsTextBased indicates whether the codec can be converted to SRT.
	IsTextBased bool
}

// GetSubtitleStreams returns info about every subtitle stream in the file.
func (m *Metadata) GetSubtitleStreams() []SubtitleStream {
	var result []SubtitleStream
	subIdx := 0
	for _, stream := range m.Streams {
		if stream.CodecType != "subtitle" {
			continue
		}
		result = append(result, SubtitleStream{
			StreamIndex:   stream.Index,
			SubtitleIndex: subIdx,
			CodecName:     stream.CodecName,
			IsTextBased:   subtitleTextCodecs[strings.ToLower(stream.CodecName)],
		})
		subIdx++
	}
	return result
}

// GoencStatus represents the processing status stored in metadata
type GoencStatus string

const (
	StatusSuccess   GoencStatus = "success"   // Successfully encoded and replaced
	StatusDiscarded GoencStatus = "discarded" // HEVC encode gave no worthwhile saving, discarded
	StatusDiscardedAV1 GoencStatus = "discarded-av1" // AV1 encode gave no worthwhile saving, discarded
	StatusFailed    GoencStatus = "failed"    // Encoding failed with error
	StatusNone      GoencStatus = ""          // Never processed by goenc
)

// HasStatus reports whether a recorded status includes want. A status can hold
// several values joined with "+", e.g. "discarded+discarded-av1".
func HasStatus(status, want GoencStatus) bool {
	for _, s := range strings.Split(string(status), "+") {
		if GoencStatus(s) == want {
			return true
		}
	}
	return false
}

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
