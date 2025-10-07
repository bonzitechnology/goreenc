package probe

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// WriteMetadata adds goenc metadata to a video file
// This creates a new file with metadata and replaces the original
func WriteMetadata(filePath string, status GoencStatus) error {
	return WriteMetadataPreserveTime(filePath, status, false)
}

// WriteMetadataPreserveTime adds goenc metadata to a video file with optional timestamp preservation
func WriteMetadataPreserveTime(filePath string, status GoencStatus, preserveTimestamp bool) error {
	// Get original file timestamp before modifying
	var originalModTime time.Time
	if preserveTimestamp {
		if info, err := os.Stat(filePath); err == nil {
			originalModTime = info.ModTime()
		}
	}

	// Create temp file for output
	tempFile := filepath.Join(filepath.Dir(filePath), ".goenc_meta_"+filepath.Base(filePath))

	// Build ffmpeg command to copy all streams and add metadata
	timestamp := time.Now().Format("2006-01-02T15:04:05")

	// Use 'comment' tag to store goenc metadata (MP4 compatible)
	// Format: goenc:status:timestamp
	metadataValue := fmt.Sprintf("goenc:%s:%s", status, timestamp)

	args := []string{
		"-i", filePath,
		// Copy all streams and metadata
		"-map", "0:v?",
		"-map", "0:a?",
		"-map", "0:s?",
		"-map", "0:t?",        // Copy attachments
		"-map_metadata", "0",  // Copy all existing metadata
		"-map_chapters", "0",  // Copy chapters
		"-c", "copy",          // Copy all codecs (no re-encoding)
		"-metadata", fmt.Sprintf("comment=%s", metadataValue),
		"-y",                  // Overwrite output
		tempFile,
	}

	cmd := exec.Command("ffmpeg", args...)

	// Run ffmpeg quietly
	if output, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tempFile) // Clean up on failure
		return fmt.Errorf("failed to write metadata: %w (output: %s)", err, string(output))
	}

	// Replace original file with metadata-updated version
	// os.Rename fails across filesystems, so use copy+delete instead
	if err := copyFile(tempFile, filePath); err != nil {
		os.Remove(tempFile)
		return fmt.Errorf("failed to copy metadata version: %w", err)
	}

	// Remove temp file
	os.Remove(tempFile)

	// Restore original timestamp if requested
	if preserveTimestamp && !originalModTime.IsZero() {
		if err := os.Chtimes(filePath, originalModTime, originalModTime); err != nil {
			return fmt.Errorf("failed to restore timestamp: %w", err)
		}
	}

	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return err
	}

	// Sync to ensure data is written to disk
	return destFile.Sync()
}
