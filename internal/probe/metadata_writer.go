package probe

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StatusComment returns the value for the 'comment' tag that marks a file as processed.
// Format: goenc:status:timestamp (MP4 compatible)
func StatusComment(status GoencStatus) string {
	return fmt.Sprintf("goenc:%s:%s", status, time.Now().Format("2006-01-02T15:04:05"))
}

// sidecarPath returns the path of the hidden status file kept next to a video
func sidecarPath(videoPath string) string {
	return filepath.Join(filepath.Dir(videoPath), "."+filepath.Base(videoPath)+".goenc")
}

// WriteSidecar records the processing status of a file we did not replace
// (failed or discarded) without touching the video itself.
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
