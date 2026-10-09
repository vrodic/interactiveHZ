package api

import (
	"testing"
	"time"
)

func TestGetCalendarCondition(t *testing.T) {
	// Monday test: 2026-10-12 is a Monday
	monTime := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	cond, args := getCalendarCondition(monTime)

	expectedCond := `(c.service_id IS NULL OR (c.start_date <= ? AND c.end_date >= ? AND c.monday = 1))`
	if cond != expectedCond {
		t.Errorf("Expected condition %s, got %s", expectedCond, cond)
	}

	if len(args) != 2 || args[0] != "20261012" || args[1] != "20261012" {
		t.Errorf("Unexpected args: %v", args)
	}

	// Saturday test: 2026-10-10 is a Saturday
	satTime := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	condSat, _ := getCalendarCondition(satTime)
	expectedSatCond := `(c.service_id IS NULL OR (c.start_date <= ? AND c.end_date >= ? AND c.saturday = 1))`
	if condSat != expectedSatCond {
		t.Errorf("Expected Saturday condition %s, got %s", expectedSatCond, condSat)
	}
}
