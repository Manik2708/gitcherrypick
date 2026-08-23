package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// JobService is the set of scheduled tasks.
//
// Exposed as an interface so the e2e harness can drive each one synchronously
// instead of sleeping — a sleep is a flake with a timer attached.
type JobService struct {
	users      port.UserRepository
	shortlists port.ShortlistRepository
	contacts   port.ContactRepository
	norms      port.NormsRepository
	hirers     port.HirerRepository
	notifier   port.Notifier
	clock      port.Clock
}

// NewJobService wires the scheduled tasks.
func NewJobService(
	users port.UserRepository,
	shortlists port.ShortlistRepository,
	contacts port.ContactRepository,
	norms port.NormsRepository,
	hirers port.HirerRepository,
	notifier port.Notifier,
	clock port.Clock,
) *JobService {
	return &JobService{users: users, shortlists: shortlists, contacts: contacts,
		norms: norms, hirers: hirers, notifier: notifier, clock: clock}
}

var _ port.JobService = (*JobService)(nil)

// LapseWarning is how long before expiry a contributor is reminded.
const LapseWarning = 3 * 24 * time.Hour

// ExpireAvailability warns contributors whose window is closing.
//
// It does NOT mutate the lapsed rows. ADR-0002 enforces expiry in the SEARCH
// QUERY so a returning contributor finds their setting intact — a job that
// cleared them would destroy exactly that.
func (s *JobService) ExpireAvailability(ctx context.Context) (int, error) {
	lapsing, err := s.users.LapsingSoon(ctx, LapseWarning, 500)
	if err != nil {
		return 0, fmt.Errorf("finding lapsing contributors: %w", err)
	}
	if len(lapsing) == 0 {
		return 0, nil
	}

	ids := make([]domain.UserID, 0, len(lapsing))
	for _, c := range lapsing {
		_ = s.notifier.Send(ctx, port.Notification{
			Kind: port.NotifyAvailabilityLapsing, Recipient: c.Email,
			Data: map[string]any{"expires_at": c.Availability.ExpiresAt},
		})
		ids = append(ids, c.ID)
	}
	if err := s.users.MarkReminded(ctx, ids); err != nil {
		return 0, fmt.Errorf("marking reminders sent: %w", err)
	}
	return len(ids), nil
}

// FlagOverdueShortlists tells admins which organizations are not delivering.
//
// Flagged, NEVER blocked. Blocking would punish a hirer for a candidate's
// silence as readily as for their own neglect (ADR-0005).
func (s *JobService) FlagOverdueShortlists(ctx context.Context) (int, error) {
	ratios, err := s.shortlists.OverdueRatios(ctx, s.clock.Now())
	if err != nil {
		return 0, fmt.Errorf("computing overdue ratios: %w", err)
	}

	flagged := 0
	for _, o := range ratios {
		if o.Ratio < OverdueFlagRatio {
			continue
		}
		_ = s.notifier.Send(ctx, port.Notification{
			Kind: port.NotifyOverdueShortlists, Recipient: "",
			Data: map[string]any{
				"organization": o.Name, "open": o.OpenShortlists,
				"overdue": o.Overdue, "ratio": o.Ratio,
			},
		})
		flagged++
	}
	return flagged, nil
}

// OverdueFlagRatio is the share of open rounds past their date that earns a
// flag (ADR-0005).
const OverdueFlagRatio = 0.8

// ExpireContactRequests lapses unanswered approaches.
func (s *JobService) ExpireContactRequests(ctx context.Context) (int, error) {
	n, err := s.contacts.ExpireStale(ctx, s.clock.Now())
	if err != nil {
		return 0, fmt.Errorf("expiring contact requests: %w", err)
	}
	return n, nil
}

// RecomputeNorms advances the shared maxima.
//
// Nightly, and it moves everyone's RELATIVE values without changing a single
// cached absolute fact.
func (s *JobService) RecomputeNorms(ctx context.Context) error {
	current, err := s.norms.Current(ctx)
	if err != nil {
		return fmt.Errorf("reading norms: %w", err)
	}

	generation := 1
	for _, n := range current {
		if n.Generation >= generation {
			generation = n.Generation + 1
		}
	}
	if err := s.norms.Recompute(ctx, generation); err != nil {
		return fmt.Errorf("recomputing norms: %w", err)
	}
	return nil
}
