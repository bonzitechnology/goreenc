package encoder

import "fmt"

// Target is the playback resolution an encode is aimed at. Sources larger
// than the target box are downscaled to fit it, preserving aspect ratio.
type Target struct {
	Name      string
	MaxWidth  int
	MaxHeight int

	// Hardware tuning for TV streamers (e.g. Google TV): HEVC level and a peak
	// bitrate cap keep streams within what hardware decoders and home networks
	// handle, and closed GOPs keep seeking reliable. Zero values mean untuned.
	Level       string // x265 level-idc, e.g. "51"
	MaxRateKbps int    // peak bitrate (VBV max rate / svt-av1 mbr)
}

var (
	// Target1080p is the default: everything is fitted into 1920x1080
	Target1080p = Target{Name: "1080p", MaxWidth: 1920, MaxHeight: 1080}

	// Target4K keeps up to 3840x2160. HEVC level 5.1 Main tier (4K60) with its
	// 40 Mbps peak, which every 4K Google TV device can hardware-decode.
	Target4K = Target{Name: "4K", MaxWidth: 3840, MaxHeight: 2160, Level: "51", MaxRateKbps: 40000}

	// Target8K keeps up to 7680x4320. HEVC level 6.1 Main tier with its
	// 120 Mbps peak. Needs an 8K TV; streaming sticks can't decode 8K.
	Target8K = Target{Name: "8K", MaxWidth: 7680, MaxHeight: 4320, Level: "61", MaxRateKbps: 120000}
)

// NeedsScale reports whether a source of this size must be downscaled
func (t Target) NeedsScale(width, height int) bool {
	return width > t.MaxWidth || height > t.MaxHeight
}

// ScaleFilter fits the video inside the target box, keeping aspect ratio and
// even dimensions. Fitting the box (not just the height) also handles
// ultrawide and portrait video correctly.
func (t Target) ScaleFilter() string {
	return fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=decrease:force_divisible_by=2", t.MaxWidth, t.MaxHeight)
}

// x265Params returns the hardware-tuning params for x265 (key=value form)
func (t Target) x265Params() []string {
	if t.Level == "" {
		return nil
	}
	return []string{
		"level-idc=" + t.Level,
		fmt.Sprintf("vbv-maxrate=%d", t.MaxRateKbps),
		fmt.Sprintf("vbv-bufsize=%d", t.MaxRateKbps),
		"open-gop=0",
	}
}

// svtAV1Params returns the hardware-tuning params for svt-av1
func (t Target) svtAV1Params() []string {
	if t.MaxRateKbps == 0 {
		return nil
	}
	return []string{fmt.Sprintf("mbr=%d", t.MaxRateKbps)} // capped CRF
}
