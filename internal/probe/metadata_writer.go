package probe

import (
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// StatusComment returns the value for the 'comment' tag that marks a file as processed.
// Format: goenc:status:timestamp (MP4 compatible)
func StatusComment(status GoencStatus) string {
	return fmt.Sprintf("goenc:%s:%s", status, time.Now().Format("2006-01-02T15:04:05"))
}

// dispositionFlags are the ffprobe disposition names that ffmpeg's -disposition accepts
var dispositionFlags = []string{
	"default", "dub", "original", "comment", "forced",
	"hearing_impaired", "visual_impaired", "captions", "descriptions",
	"attached_pic",
}

// DispositionArg formats ffprobe disposition flags as a -disposition value
func DispositionArg(disposition map[string]int) string {
	var set []string
	for _, flag := range dispositionFlags {
		if disposition[flag] == 1 {
			set = append(set, flag)
		}
	}
	if len(set) == 0 {
		return "0"
	}
	return strings.Join(set, "+")
}

// WriteStatusTag embeds the goenc status in a file's 'comment' tag by remuxing it.
// The remux goes to a temp file in the same directory and only replaces the
// original (via atomic rename) after it has been verified to contain the same
// streams and duration. On any error the original is left untouched.
func WriteStatusTag(filePath string, status GoencStatus, preserveTimestamp bool) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return err
	}

	meta, err := GetMetadata(filePath)
	if err != nil {
		return err
	}
	srcDuration, err := meta.GetDuration()
	if err != nil {
		return fmt.Errorf("cannot verify remux: %w", err)
	}

	// Keep the original extension so ffmpeg picks the same container
	tmp, err := os.CreateTemp(filepath.Dir(filePath), ".goenc_tmp_*"+filepath.Ext(filePath))
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath) // no-op once renamed

	args := []string{
		"-v", "error",
		"-i", filePath,
		"-map", "0", // Every stream, so nothing is lost; unsupported streams make this fail rather than drop
		"-map_metadata", "0",
		"-map_chapters", "0",
		"-c", "copy",
		"-metadata", "comment=" + StatusComment(status),
	}
	if len(meta.Streams) > 0 {
		// Setting any disposition explicitly stops ffmpeg from marking the first
		// subtitle track as default when the source has none
		args = append(args, "-disposition:0", DispositionArg(meta.Streams[0].Disposition))
	}
	args = append(args, "-y", tmpPath)
	if output, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("remux failed: %w (output: %s)", err, strings.TrimSpace(string(output)))
	}

	// Verify the remux before it replaces the original
	outMeta, err := GetMetadata(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to probe remux: %w", err)
	}
	if len(outMeta.Streams) != len(meta.Streams) {
		return fmt.Errorf("remux has %d streams, original has %d", len(outMeta.Streams), len(meta.Streams))
	}
	outDuration, err := outMeta.GetDuration()
	if err != nil {
		return fmt.Errorf("failed to read remux duration: %w", err)
	}
	if math.Abs(outDuration-srcDuration) > math.Max(2.0, srcDuration*0.01) {
		return fmt.Errorf("remux duration %.1fs doesn't match original %.1fs", outDuration, srcDuration)
	}

	if err := os.Chmod(tmpPath, info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		return err
	}

	if preserveTimestamp {
		if err := os.Chtimes(filePath, info.ModTime(), info.ModTime()); err != nil {
			return fmt.Errorf("status written but failed to restore timestamp: %w", err)
		}
	}
	return nil
}

// sidecarPath returns the path of the hidden status file kept next to a video
func sidecarPath(videoPath string) string {
	return filepath.Join(filepath.Dir(videoPath), "."+filepath.Base(videoPath)+".goenc")
}

// WriteSidecar records a file's processing status without touching the video.
// Used when the status can't be embedded with WriteStatusTag.
func WriteSidecar(videoPath string, status GoencStatus) error {
	return os.WriteFile(sidecarPath(videoPath), []byte(StatusComment(status)+"\n"), 0644)
}

// ReadSidecar returns the status and timestamp recorded in a video's sidecar file,
// or StatusNone if there is none.
func ReadSidecar(videoPath string) (GoencStatus, string) {
	data, err := os.ReadFile(sidecarPath(videoPath))
	if err != nil {
		return StatusNone, ""
	}
	parts := splitN(strings.TrimSpace(string(data)), ":", 3)
	if len(parts) < 3 || parts[0] != "goenc" {
		return StatusNone, ""
	}
	return GoencStatus(parts[1]), parts[2]
}

// RemoveSidecar deletes a video's sidecar status file if present
func RemoveSidecar(videoPath string) {
	os.Remove(sidecarPath(videoPath))
}

// CopyFileAtomic copies src to dst without ever leaving dst partially written.
// The data is copied to a temp file in dst's directory and then renamed over dst,
// so an interrupted copy leaves any existing dst untouched.
func CopyFileAtomic(src, dst string) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".goenc_tmp_*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once renamed

	sourceFile, err := os.Open(src)
	if err != nil {
		tmp.Close()
		return err
	}
	defer sourceFile.Close()

	if _, err := io.Copy(tmp, sourceFile); err != nil {
		tmp.Close()
		return err
	}
	// Sync to ensure data is written to disk before the rename
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// CreateTemp uses 0600; keep the replaced file's permissions, or default to 0644
	mode := os.FileMode(0644)
	if info, err := os.Stat(dst); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return err
	}

	return os.Rename(tmpPath, dst)
}
