package api

import (
	"strings"
	"testing"
	"time"
)

func TestGetCalendarCondition(t *testing.T) {
	// Monday test: 2026-10-12 is a Monday
	monTime := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	cond, args := getCalendarCondition(monTime)

	if !strings.Contains(cond, "c.monday = 1") || !strings.Contains(cond, "calendar_dates") {
		t.Errorf("Unexpected calendar condition: %s", cond)
	}

	if len(args) != 4 || args[0] != "20261012" {
		t.Errorf("Unexpected args: %v", args)
	}

	// Saturday test: 2026-10-10 is a Saturday
	satTime := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	condSat, _ := getCalendarCondition(satTime)
	if !strings.Contains(condSat, "c.saturday = 1") {
		t.Errorf("Unexpected Saturday condition: %s", condSat)
	}
}
