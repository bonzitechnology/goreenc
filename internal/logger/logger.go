package logger

import (
	"fmt"
	"io"
	"log"
	"os"
)

type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

type Logger struct {
	level   Level
	logFile *os.File
}

func New(level Level) *Logger {
	return &Logger{level: level}
}

// SetLogFile sets a log file for output (appends to file)
func (l *Logger) SetLogFile(path string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	l.logFile = file
	// Write to both stdout and file
	log.SetOutput(io.MultiWriter(os.Stdout, file))
	return nil
}

// Close closes the log file if open
func (l *Logger) Close() error {
	if l.logFile != nil {
		return l.logFile.Close()
	}
	return nil
}

func (l *Logger) Debug(format string, v ...interface{}) {
	if l.level <= LevelDebug {
		log.Printf("[DEBUG] "+format, v...)
	}
}

func (l *Logger) Info(format string, v ...interface{}) {
	if l.level <= LevelInfo {
		log.Printf("[INFO] "+format, v...)
	}
}

func (l *Logger) Warn(format string, v ...interface{}) {
	if l.level <= LevelWarn {
		log.Printf("[WARN] "+format, v...)
	}
}

func (l *Logger) Error(format string, v ...interface{}) {
	if l.level <= LevelError {
		log.Printf("[ERROR] "+format, v...)
	}
}

func (l *Logger) Fatal(format string, v ...interface{}) {
	log.Printf("[FATAL] "+format, v...)
	os.Exit(1)
}

// Progress prints a line that can be overwritten (for encoding progress)
func (l *Logger) Progress(format string, v ...interface{}) {
	if l.level <= LevelInfo {
		msg := fmt.Sprintf(format, v...)

		// Write to stdout with carriage return (overwrites previous line)
		fmt.Fprintf(os.Stdout, "\r%s", msg)
	}
}

// ProgressDone clears the progress line and moves to next line
func (l *Logger) ProgressDone() {
	if l.level <= LevelInfo {
		fmt.Fprintln(os.Stdout)
	}
}
