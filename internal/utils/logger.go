package utils

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	ErrorLogger    *log.Logger
	logFile        *os.File
	loggerMu       sync.Mutex
	currentLogDate string
)

// InitLogger prepares the error logger.
// The actual log file is created lazily on the first LogError call.
func InitLogger() error {
	return nil
}

// ensureLogFile creates or rotates the error log file as needed.
// Must be called with loggerMu held.
func ensureLogFile() error {
	today := time.Now().Format("2006-01-02")

	// File already open for today
	if logFile != nil && currentLogDate == today {
		return nil
	}

	// Close old file if date changed
	if logFile != nil {
		logFile.Close()
		logFile = nil
		ErrorLogger = nil
	}

	logsDir := "logs"
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return fmt.Errorf("failed to create logs directory: %v", err)
	}

	filename := fmt.Sprintf("error_%s.txt", today)
	logPath := filepath.Join(logsDir, filename)

	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %v", err)
	}

	logFile = file
	currentLogDate = today
	ErrorLogger = log.New(file, "[ERROR] ", log.Ldate|log.Ltime|log.Lshortfile)
	return nil
}

// LogError logs an error to both console and the error log file.
// The error log file is created lazily on the first call.
func LogError(format string, v ...interface{}) {
	// Always print to console
	log.Printf("[ERROR] "+format, v...)

	loggerMu.Lock()
	defer loggerMu.Unlock()

	if err := ensureLogFile(); err != nil {
		log.Printf("Failed to create error log file: %v", err)
		return
	}

	if ErrorLogger != nil {
		ErrorLogger.Printf(format, v...)
	}
}

// CloseLogger closes the error log file
func CloseLogger() {
	loggerMu.Lock()
	defer loggerMu.Unlock()

	if logFile != nil {
		logFile.Close()
		logFile = nil
	}
	ErrorLogger = nil
	currentLogDate = ""
}
