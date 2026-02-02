package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"tms-backend/internal/utils"
)

// TempLogPayload - ข้อมูลที่ส่งไป Legacy API ทุก 5 นาที
type TempLogPayload struct {
	McuID     string  `json:"mcuId"`
	Status    string  `json:"status"`
	TempValue float64 `json:"tempValue"`
	RealValue int     `json:"realValue"`
	Date      string  `json:"date"`
	Time      string  `json:"time"`
}

// AlertPayload - ข้อมูลที่ส่งไป Alert API เมื่อมี alert
type AlertPayload struct {
	McuID       string  `json:"mcuId"`
	Status      string  `json:"status"`
	TempValue   float64 `json:"tempValue"`
	RealValue   int     `json:"realValue"`
	Date        string  `json:"date"`
	Time        string  `json:"time"`
	Message     string  `json:"message"`
	AlertType   string  `json:"alertType"` // "high", "low", "normal"
	MachineName string  `json:"machineName"`
	ProbeNo     int     `json:"probeNo"`
	MinTemp     float64 `json:"minTemp"`
	MaxTemp     float64 `json:"maxTemp"`
}

// APINotificationService handles sending data to external APIs
type APINotificationService struct {
	legacyAPIURL   string
	legacyAPIToken string
	httpClient     *http.Client
}

// NewAPINotificationService creates a new API notification service
func NewAPINotificationService() *APINotificationService {
	return &APINotificationService{
		legacyAPIURL:   os.Getenv("LEGACY_API_URL"),
		legacyAPIToken: os.Getenv("LEGACY_API_TOKEN"),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// IsLegacyAPIEnabled checks if Legacy API is configured
func (s *APINotificationService) IsLegacyAPIEnabled() bool {
	return s.legacyAPIURL != "" && s.legacyAPIToken != ""
}

// SendTempLog sends temperature log to Legacy API (called every 5 minutes)
func (s *APINotificationService) SendTempLog(payload TempLogPayload) error {
	if !s.IsLegacyAPIEnabled() {
		return nil // Skip if not configured
	}

	url := fmt.Sprintf("%s/legacy/templog", s.legacyAPIURL)
	return s.sendRequest(url, s.legacyAPIToken, payload, "temp log")
}

// SendTempLogBatch sends multiple temperature logs to Legacy API
func (s *APINotificationService) SendTempLogBatch(payloads []TempLogPayload) error {
	if !s.IsLegacyAPIEnabled() {
		return nil
	}

	url := fmt.Sprintf("%s/legacy/templog/batch", s.legacyAPIURL)
	return s.sendRequest(url, s.legacyAPIToken, payloads, "temp log batch")
}

// SendAlert sends alert notification to Legacy API
func (s *APINotificationService) SendAlert(payload AlertPayload) error {
	if !s.IsLegacyAPIEnabled() {
		return nil // Skip if not configured
	}

	url := fmt.Sprintf("%s/legacy/templog/alert/notification", s.legacyAPIURL)
	return s.sendRequest(url, s.legacyAPIToken, payload, "alert notification")
}

// sendRequest is a helper function to send HTTP POST request
func (s *APINotificationService) sendRequest(url, token string, payload interface{}, description string) error {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		utils.LogError("API Notification - Failed to marshal %s payload: %v", description, err)
		return fmt.Errorf("failed to marshal %s payload: %w", description, err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		utils.LogError("API Notification - Failed to create %s request: %v", description, err)
		return fmt.Errorf("failed to create %s request: %w", description, err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := s.httpClient.Do(req)
	if err != nil {
		utils.LogError("API Notification - Failed to send %s to %s: %v", description, url, err)
		return fmt.Errorf("failed to send %s: %w", description, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		utils.LogError("API Notification - %s request failed with status %d (url=%s)", description, resp.StatusCode, url)
		return fmt.Errorf("%s request failed with status: %d", description, resp.StatusCode)
	}

	log.Printf("✅ Successfully sent %s to API", description)
	return nil
}

// Global instance
var GlobalAPINotificationService *APINotificationService

func init() {
	GlobalAPINotificationService = NewAPINotificationService()
}
