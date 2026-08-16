package domain_test

import (
	"testing"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
)

// Pure arithmetic, so it needs no database — the reason it lives in domain.

func TestCooldownFor(t *testing.T) {
	day := 24 * time.Hour
	for _, tt := range []struct {
		tier int
		days int
	}{
		{0, 0}, {1, 28}, {2, 56}, {3, 112}, {4, 224},
		// Capped. Past about a year a cooldown is a ban, and a ban should be an
		// admin decision with a reason.
		{5, 365}, {6, 365}, {20, 365},
	} {
		if got := domain.CooldownFor(tt.tier); got != time.Duration(tt.days)*day {
			t.Errorf("tier %d: expected %d days, got %s", tt.tier, tt.days, got)
		}
	}
}

func TestRecordRejection(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

	t.Run("three rejections trigger the first tier", func(t *testing.T) {
		c := &domain.Cooldown{}
		for i := 1; i <= 2; i++ {
			c.RecordRejection(now)
			if c.CooldownUntil != nil {
				t.Fatalf("rejection %d should not trigger a cooldown", i)
			}
			if c.Tier != 0 {
				t.Fatalf("rejection %d moved the tier to %d", i, c.Tier)
			}
		}
		c.RecordRejection(now)
		if c.Tier != 1 {
			t.Errorf("expected tier 1, got %d", c.Tier)
		}
		if c.CooldownUntil == nil || !c.CooldownUntil.Equal(now.Add(28*24*time.Hour)) {
			t.Errorf("expected a 28-day cooldown, got %v", c.CooldownUntil)
		}
		// The counter resets so the NEXT three rejections earn a longer wait.
		if c.RejectionCount != 0 {
			t.Errorf("expected the counter reset, got %d", c.RejectionCount)
		}
	})

	t.Run("the tier survives the reset, so cooldowns double", func(t *testing.T) {
		c := &domain.Cooldown{}
		for range 3 {
			c.RecordRejection(now)
		}
		for range 3 {
			c.RecordRejection(now)
		}
		if c.Tier != 2 {
			t.Errorf("expected tier 2, got %d", c.Tier)
		}
		if !c.CooldownUntil.Equal(now.Add(56 * 24 * time.Hour)) {
			t.Errorf("expected 56 days at tier 2, got %v", c.CooldownUntil)
		}
	})

	t.Run("CanRequest gates on the expiry", func(t *testing.T) {
		c := &domain.Cooldown{}
		if !c.CanRequest(now) {
			t.Error("a contributor who has never disputed may request")
		}
		for range 3 {
			c.RecordRejection(now)
		}
		if c.CanRequest(now) {
			t.Error("expected a request to be refused during the cooldown")
		}
		if !c.CanRequest(now.Add(29 * 24 * time.Hour)) {
			t.Error("expected the cooldown to lapse")
		}
	})
}
