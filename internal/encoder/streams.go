package encoder

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/kronicd/goreenc/internal/probe"
)

// streamAction is what happens to one input stream during the encode
type streamAction int

const (
	actionCopy   streamAction = iota // stream copy unchanged
	actionEncode                     // re-encode (real video)
	actionToSRT                      // convert subtitles MKV can't hold to SRT
)

// plannedStream is an input stream and what to do with it
type plannedStream struct {
	probe.Stream
	Action streamAction
}

// streamPlan describes how every input stream ends up in the MKV output
type streamPlan struct {
	Mapped      []plannedStream // mapped in source order; output index = slice index
	CoverArt    []probe.Stream  // re-added as MKV attachments
	Unsupported []probe.Stream  // can't be stored in MKV at all
}

// planStreams decides how to carry every stream of the input into MKV.
//
// Rather than relying on a fixed list of what Matroska supports, it does a
// trial mux of the first second with this ffmpeg build. Streams that fail are
// converted to SRT if they are subtitles ffmpeg can read as text, and are
// otherwise reported as unsupported.
func planStreams(inputPath string, metadata *probe.Metadata) (*streamPlan, error) {
	plan := &streamPlan{}
	var candidates []int // indexes into plan.Mapped of streams to trial-mux
	for _, s := range metadata.Streams {
		switch {
		case s.IsCoverArt():
			if probe.CanAttachPic(s) {
				plan.CoverArt = append(plan.CoverArt, s)
			} else {
				plan.Unsupported = append(plan.Unsupported, s)
			}
		case s.CodecType == "video":
			plan.Mapped = append(plan.Mapped, plannedStream{Stream: s, Action: actionEncode})
		default:
			candidates = append(candidates, len(plan.Mapped))
			plan.Mapped = append(plan.Mapped, plannedStream{Stream: s, Action: actionCopy})
		}
	}

	if len(candidates) == 0 {
		return plan, nil
	}
	all := make([]plannedStream, len(candidates))
	for i, c := range candidates {
		all[i] = plan.Mapped[c]
	}
	if trialMux(inputPath, all) == nil {
		return plan, nil
	}

	// Something failed: test streams one at a time to find which
	unsupported := map[int]bool{}
	for _, c := range candidates {
		ps := plan.Mapped[c]
		if trialMux(inputPath, []plannedStream{ps}) == nil {
			continue
		}
		if ps.CodecType == "subtitle" {
			ps.Action = actionToSRT
			if trialMux(inputPath, []plannedStream{ps}) == nil {
				plan.Mapped[c] = ps
				continue
			}
		}
		unsupported[c] = true
	}

	var kept []plannedStream
	for i, ps := range plan.Mapped {
		if unsupported[i] {
			plan.Unsupported = append(plan.Unsupported, ps.Stream)
		} else {
			kept = append(kept, ps)
		}
	}
	plan.Mapped = kept
	return plan, nil
}

// trialMux stream-copies (or converts) the given streams' first second into
// Matroska, discarding the output, to check this ffmpeg can store them
func trialMux(inputPath string, streams []plannedStream) error {
	args := []string{"-v", "error", "-i", inputPath}
	for _, ps := range streams {
		args = append(args, "-map", "0:"+strconv.Itoa(ps.Index))
	}
	args = append(args, "-c", "copy")
	for i, ps := range streams {
		if ps.Action == actionToSRT {
			args = append(args, "-c:"+strconv.Itoa(i), "srt")
		}
	}
	args = append(args, "-t", "1", "-f", "matroska", "-y", os.DevNull)

	if output, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// expectedStreams is how many streams the output should have
func (p *streamPlan) expectedStreams() int {
	return len(p.Mapped) + len(p.CoverArt)
}

// describe returns a short description of a stream for log messages
func describe(s probe.Stream) string {
	codec := s.CodecName
	if codec == "" {
		codec = s.CodecTagString // e.g. "tmcd" for MP4 timecode tracks
	}
	return fmt.Sprintf("%d (%s %s)", s.Index, s.CodecType, codec)
}
