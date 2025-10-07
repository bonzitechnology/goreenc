package scanner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ScanOptions holds configuration for the scanner
type ScanOptions struct {
	MinSizeMB  int64
	Override   bool // Re-encode files already in HEVC
}

// VideoFile represents a discovered video file
type VideoFile struct {
	Path string
	Size int64
}

// Scan walks a directory tree and returns all video files matching criteria
func Scan(rootPath string, opts ScanOptions) ([]VideoFile, error) {
	var videos []VideoFile

	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Check if it's a video file
		if !isVideoFile(path) {
			return nil
		}

		// Check size threshold
		sizeMB := info.Size() / (1024 * 1024)
		if sizeMB < opts.MinSizeMB {
			return nil
		}

		videos = append(videos, VideoFile{
			Path: path,
			Size: info.Size(),
		})

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to scan directory: %w", err)
	}

	return videos, nil
}

// ScanStream walks a directory tree and sends video files to a channel as they're found
func ScanStream(rootPath string, opts ScanOptions) (<-chan VideoFile, <-chan error) {
	videos := make(chan VideoFile)
	errs := make(chan error, 1)

	go func() {
		defer close(videos)
		defer close(errs)

		err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// Skip directories
			if info.IsDir() {
				return nil
			}

			// Check if it's a video file
			if !isVideoFile(path) {
				return nil
			}

			// Check size threshold
			sizeMB := info.Size() / (1024 * 1024)
			if sizeMB < opts.MinSizeMB {
				return nil
			}

			// Send to channel
			videos <- VideoFile{
				Path: path,
				Size: info.Size(),
			}

			return nil
		})

		if err != nil {
			errs <- fmt.Errorf("failed to scan directory: %w", err)
		}
	}()

	return videos, errs
}

// isVideoFile checks if a file is a video using the 'file' command
func isVideoFile(path string) bool {
	cmd := exec.Command("file", "--mime-type", "-b", path)
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	mimeType := strings.TrimSpace(string(output))
	return strings.HasPrefix(mimeType, "video/")
}

// FormatSize returns a human-readable size string
func FormatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// FormatSizeMB returns size in MB
func FormatSizeMB(bytes int64) int64 {
	return bytes / (1024 * 1024)
}
