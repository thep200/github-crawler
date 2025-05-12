package crawler

import (
	"strings"
	"time"
)

// Time-base
func generateTimeWindows() []timeWindow {
	windows := []timeWindow{
		{
			startDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Now(),
			processed: false,
		},
		{
			startDate: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		{
			startDate: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		{
			startDate: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		{
			startDate: time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		{
			startDate: time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		{
			startDate: time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		{
			startDate: time.Date(2007, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
	}
	return windows
}

// Phân tích full_name để lấy user và repo name
func extractUserAndRepo(fullName string) (string, string) {
	parts := strings.Split(fullName, "/")
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "", ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
