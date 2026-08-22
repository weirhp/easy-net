package model

import (
	"testing"
	"time"
)

func TestNormalizeRefreshMinutes(t *testing.T) {
	cases := map[int]int{0: 0, 30: 30, 60: 60, 180: 180, 360: 360, 1440: 1440, 15: 60, -1: 60}
	for input, want := range cases {
		if got := NormalizeRefreshMinutes(input); got != want {
			t.Fatalf("NormalizeRefreshMinutes(%d) = %d, want %d", input, got, want)
		}
	}
}

func TestRefreshDue(t *testing.T) {
	now := time.Now()
	if (Subscription{RefreshMinutes: 60, UpdatedAt: now}).RefreshDue() {
		t.Fatal("fresh subscription should not be due")
	}
	if !(Subscription{RefreshMinutes: 60, UpdatedAt: now.Add(-2 * time.Hour)}).RefreshDue() {
		t.Fatal("expected due after interval")
	}
	if (Subscription{RefreshMinutes: 0, UpdatedAt: now.Add(-24 * time.Hour)}).RefreshDue() {
		t.Fatal("never should not be due")
	}
}
