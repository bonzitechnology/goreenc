package encoder

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kronicd/goreenc/internal/probe"
)

const (
	sampleCount  = 4    // number of sample clips
	sampleLength = 20.0 // seconds per sample clip
)

// estimateSavings encodes a few short clips spread through the file with the
// real settings and extrapolates the percentage of the file an encode would save.
// Clips sit at 20/40/60/80% of the duration to avoid intros and credits.
// Returns ok=false if the file is too short to sample meaningfully.
func (e *Encoder) estimateSavings(inputPath string, metadata *probe.Metadata, profile Profile, fileSize int64, duration float64, runDir string) (percent float64, ok bool, err error) {
	if duration < sampleCount*sampleLength*2 {
		return 0, false, nil
	}
	videoIndex := metadata.PrimaryVideoIndex()
	if videoIndex < 0 {
		return 0, false, fmt.Errorf("no video stream")
	}

	samplePath := filepath.Join(runDir, "sample.mkv")
	defer os.Remove(samplePath)

	var srcBytes, encBytes int64
	for i := 0; i < sampleCount; i++ {
		start := duration * float64(i+1) / float64(sampleCount+1)
		e.log.Progress("  Estimating savings... [sample %d/%d]", i+1, sampleCount)

		cmd := BuildSampleCommand(inputPath, samplePath, metadata, profile, e.opts.UseAV1, e.opts.Target, start, sampleLength)
		e.log.Debug("  sample command: %s", cmd.String())
		if output, err := cmd.CombinedOutput(); err != nil {
			return 0, false, fmt.Errorf("sample encode failed: %w (output: %s)", err, output)
		}

		src, err := probe.PacketBytes(inputPath, videoIndex, start, start+sampleLength)
		if err != nil {
			return 0, false, err
		}
		enc, err := probe.PacketBytes(samplePath, 0, 0, 0)
		if err != nil {
			return 0, false, err
		}
		srcBytes += src
		encBytes += enc
	}
	e.log.ProgressDone()

	if srcBytes == 0 {
		return 0, false, fmt.Errorf("no source video packets found in sample ranges")
	}

	// Only the video is re-encoded; audio and subtitles are copied unchanged
	ratio := float64(encBytes) / float64(srcBytes)
	videoBytes := metadata.EstimateVideoBytes(fileSize)
	saved := float64(videoBytes) * (1 - ratio)
	return saved / float64(fileSize) * 100, true, nil
}
