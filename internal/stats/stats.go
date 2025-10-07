package stats

import (
	"encoding/csv"
	"fmt"
	"os"
	"time"

	"github.com/kronicd/goreenc/internal/encoder"
)

// Writer handles CSV stats export
type Writer struct {
	file   *os.File
	writer *csv.Writer
}

// New creates a new stats writer
func New(filename string) (*Writer, error) {
	file, err := os.Create(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to create stats file: %w", err)
	}

	writer := csv.NewWriter(file)

	// Write header
	header := []string{
		"Filename",
		"Original Size (MB)",
		"Encoded Size (MB)",
		"Saved (MB)",
		"Saved (%)",
		"Resolution",
		"Duration (s)",
		"Encode Duration",
		"Status",
		"Error",
	}
	if err := writer.Write(header); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to write header: %w", err)
	}

	return &Writer{
		file:   file,
		writer: writer,
	}, nil
}

// WriteResult writes a single result to the CSV
func (w *Writer) WriteResult(result *encoder.Result) error {
	errMsg := ""
	if result.Error != nil {
		errMsg = result.Error.Error()
	}

	record := []string{
		result.InputPath,
		fmt.Sprintf("%.2f", float64(result.OriginalSize)/(1024*1024)),
		fmt.Sprintf("%.2f", float64(result.EncodedSize)/(1024*1024)),
		fmt.Sprintf("%.2f", float64(result.Saved)/(1024*1024)),
		fmt.Sprintf("%.2f", result.SavedPercent),
		result.Resolution,
		fmt.Sprintf("%.2f", result.Duration),
		result.EncodeDuration.Round(time.Second).String(),
		result.Status,
		errMsg,
	}

	if err := w.writer.Write(record); err != nil {
		return err
	}

	// Flush after each write so data is written immediately
	w.writer.Flush()
	return w.writer.Error()
}

// Close flushes and closes the stats file
func (w *Writer) Close() error {
	w.writer.Flush()
	if err := w.writer.Error(); err != nil {
		w.file.Close()
		return err
	}
	return w.file.Close()
}
