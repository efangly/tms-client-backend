package utils

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

var (
	ErrorLogger *log.Logger
	logFile     *os.File
)

// InitLogger initializes the logger to write to both console and file
func InitLogger() error {
	// Create logs directory if it doesn't exist
	logsDir := "logs"
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return fmt.Errorf("failed to create logs directory: %v", err)
	}

	// Create log file with current date
	now := time.Now()
	filename := fmt.Sprintf("error_%s.txt", now.Format("2006-01-02"))
	logPath := filepath.Join(logsDir, filename)

	// Open or create log file (append mode)
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %v", err)
	}

	logFile = file

	// Create multi-writer to write to both file and stdout
	multiWriter := io.MultiWriter(os.Stdout, file)

	// Initialize error logger
	ErrorLogger = log.New(multiWriter, "[ERROR] ", log.Ldate|log.Ltime|log.Lshortfile)

	log.Printf("✅ Error logging initialized: %s", logPath)

	return nil
}

// LogError logs an error message to both console and file
func LogError(format string, v ...interface{}) {
	if ErrorLogger != nil {
		ErrorLogger.Printf(format, v...)
	} else {
		// Fallback to standard logger if not initialized
		log.Printf("[ERROR] "+format, v...)
	}
}

// CloseLogger closes the log file
func CloseLogger() {
	if logFile != nil {
		logFile.Close()
	}
}

// RotateLogFile rotates the log file if date has changed
func RotateLogFile() error {
	if logFile != nil {
		logFile.Close()
	}
	return InitLogger()
}
