package services

import (
	"testing"
	"time"
)

func TestDurationFromEnv(t *testing.T) {
	const key = "TEST_DURATION_ENV"
	def := 5 * time.Minute

	cases := []struct {
		name string
		val  string
		want time.Duration
	}{
		{"unset", "", def},
		{"valid seconds", "30s", 30 * time.Second},
		{"valid minutes", "2m", 2 * time.Minute},
		{"invalid", "abc", def},
		{"zero", "0s", def},
		{"negative", "-5s", def},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(key, tc.val)
			if got := durationFromEnv(key, def); got != tc.want {
				t.Errorf("durationFromEnv(%q) = %v, want %v", tc.val, got, tc.want)
			}
		})
	}
}
