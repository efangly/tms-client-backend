package utils

import (
	"log"
	"net"
	"time"
)

// WaitForNetwork waits for network connectivity before proceeding
// Returns true if network is ready, false if timeout exceeded
func WaitForNetwork(timeout time.Duration) bool {
	log.Println("Checking network connectivity...")
	deadline := time.Now().Add(timeout)
	attempt := 0

	for time.Now().Before(deadline) {
		attempt++

		// Try to resolve a reliable DNS name
		if _, err := net.LookupHost("google.com"); err == nil {
			log.Printf("Network is ready (attempt %d)", attempt)
			return true
		}

		// Also try alternative DNS
		if _, err := net.LookupHost("cloudflare.com"); err == nil {
			log.Printf("Network is ready (attempt %d)", attempt)
			return true
		}

		log.Printf("Network not ready yet (attempt %d), retrying...", attempt)
		time.Sleep(2 * time.Second)
	}

	log.Printf("Network check timeout after %v", timeout)
	return false
}

// RetryWithBackoff retries a function with exponential backoff
func RetryWithBackoff(
	operation string,
	fn func() error,
	maxAttempts int,
	initialDelay time.Duration,
	maxDelay time.Duration,
) error {
	var lastErr error
	delay := initialDelay

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		log.Printf("%s (attempt %d/%d)...", operation, attempt, maxAttempts)

		err := fn()
		if err == nil {
			if attempt > 1 {
				log.Printf("%s succeeded after %d attempts", operation, attempt)
			}
			return nil
		}

		lastErr = err
		log.Printf("%s failed: %v", operation, err)

		if attempt < maxAttempts {
			log.Printf("Retrying in %v...", delay)
			time.Sleep(delay)

			// Exponential backoff
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}

	log.Printf("%s failed after %d attempts", operation, maxAttempts)
	return lastErr
}
