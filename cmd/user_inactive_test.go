package cmd

import (
	"testing"
	"time"
)

func TestResolveInactiveLastTimeUsesCreateTimeWhenLastlogMissing(t *testing.T) {
	now := time.Date(2026, time.April, 15, 10, 30, 0, 0, time.UTC)
	createTime := time.Date(2025, time.April, 25, 8, 0, 0, 0, time.UTC)

	lastTime := resolveInactiveLastTime(now, time.Time{}, createTime, time.Time{}, false, false)

	if !lastTime.Equal(createTime) {
		t.Fatalf("expected create time %v, got %v", createTime, lastTime)
	}

	display := formatInactiveLastLogin(lastTime, createTime, false)
	if display != "2025-04-25" {
		t.Fatalf("expected create date display, got %q", display)
	}
}

func TestResolveInactiveLastTimePrefersTodayForRunningProcesses(t *testing.T) {
	now := time.Date(2026, time.April, 15, 10, 30, 0, 0, time.UTC)
	createTime := time.Date(2025, time.April, 25, 8, 0, 0, 0, time.UTC)
	want := time.Date(2026, time.April, 15, 0, 0, 0, 0, time.UTC)

	lastTime := resolveInactiveLastTime(now, time.Time{}, createTime, time.Time{}, false, true)

	if !lastTime.Equal(want) {
		t.Fatalf("expected running process to promote last time to %v, got %v", want, lastTime)
	}
}

func TestParseLastlogOutputParsesDatesAndSkipsNeverLoggedIn(t *testing.T) {
	userSet := map[string]bool{
		"alice": true,
		"bob":   true,
		"carol": true,
	}

	output := "" +
		"Username         Port     From                                       Latest\n" +
		"alice            pts/0    10.0.0.1                                  Wed Apr 15 16:48:35 +0800 2026\n" +
		"bob                                                          **Never logged in**\n" +
		"carol                                                    Never logged in\n"

	result := parseLastlogOutput(output, userSet)

	if len(result) != 1 {
		t.Fatalf("expected only alice to have a parsed login, got %d entries", len(result))
	}

	got, ok := result["alice"]
	if !ok {
		t.Fatalf("expected alice to be parsed")
	}

	want := time.Date(2026, time.April, 15, 16, 48, 35, 0, time.FixedZone("", 8*60*60))
	if !got.Equal(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}

	if _, ok := result["bob"]; ok {
		t.Fatalf("expected bob to be skipped for never-logged-in output")
	}
	if _, ok := result["carol"]; ok {
		t.Fatalf("expected carol to be skipped for plain never-logged-in output")
	}
}
