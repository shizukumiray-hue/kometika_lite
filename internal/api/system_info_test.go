package api

import (
	"testing"
	"time"
)

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		d        time.Duration
		expected string
	}{
		{0, "N/A"},
		{45 * time.Second, "45s"},
		{2*time.Minute + 15*time.Second, "2m 15s"},
		{3*time.Hour + 20*time.Minute + 10*time.Second, "3h 20m 10s"},
		{24*time.Hour + 10*time.Hour + 15*time.Minute + 30*time.Second, "1d 10h 15m 30s"},
		{586*time.Hour + 10*time.Minute, "24d 10h 10m 0s"},
	}

	for _, tc := range tests {
		got := FormatUptime(tc.d)
		if got != tc.expected {
			t.Errorf("FormatUptime(%v) = %s, expected %s", tc.d, got, tc.expected)
		}
	}
}

func TestGetSystemInfo(t *testing.T) {
	info, err := GetSystemInfoExtended()
	if err != nil {
		t.Fatalf("GetSystemInfoExtended failed: %v", err)
	}
	if info.CPUCores <= 0 {
		t.Errorf("expected CPUCores > 0, got %d", info.CPUCores)
	}
	text := FormatSystemInfoText(info)
	if text == "" {
		t.Errorf("expected non-empty formatted text")
	}
	t.Logf("Formatted system info output:\n%s", text)
}
