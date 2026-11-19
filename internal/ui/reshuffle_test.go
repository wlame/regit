package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/safety"
)

// pickedDaysOnCalendar opens the redate flow on Picked Days with the calendar
// focused and picks n consecutive days, starting at the cursor.
func pickedDaysOnCalendar(t *testing.T, m Model, n int) Model {
	t.Helper()
	m = selectStrategy(m, 2) // Picked Days
	m = send(m, "c")         // focus the calendar
	for i := range n {
		if i > 0 {
			m = send(m, "down")
		}
		m = send(m, " ")
	}
	return m
}

// countsOnPickedDays renders the per-day commit counts of the picked days, in
// day order, for comparing two distributions.
func countsOnPickedDays(m Model) string {
	counts := m.dayCounts()
	var days []string
	for day := range m.activeStrat().picked {
		days = append(days, day)
	}
	var b strings.Builder
	slices.Sort(days)
	for _, day := range days {
		fmt.Fprintf(&b, "%s=%d ", day, counts[day])
	}
	return b.String()
}

// TestPickedDays_eachToggleDrawsNewNumbers: picking a day and unpicking it
// again returns to the same set of days, but with a new random split.
func TestPickedDays_eachToggleDrawsNewNumbers(t *testing.T) {
	m := pickedDaysOnCalendar(t, newTestModelN(t, 30), 3)
	before, seedBefore := countsOnPickedDays(m), m.redateSeed

	m = send(m, "down", " ", " ") // pick a fourth day, then unpick it

	if m.redateSeed == seedBefore {
		t.Fatal("a pick toggle should draw a new seed")
	}
	if after := countsOnPickedDays(m); after == before {
		t.Errorf("same days, same numbers after two toggles: %s", after)
	}
}

func TestRedateFlow_rReshufflesWithoutChangingTheDays(t *testing.T) {
	m := pickedDaysOnCalendar(t, newTestModelN(t, 30), 3)
	picked, cursor, before := len(m.activeStrat().picked), m.calCursor, countsOnPickedDays(m)

	m = send(m, "r")

	if len(m.activeStrat().picked) != picked || !m.calCursor.Equal(cursor) || m.focus != focusCalendar {
		t.Fatal("r must not change the picked days, the cursor, or the focus")
	}
	if after := countsOnPickedDays(m); after == before {
		t.Errorf("r should draw a new split: %s", after)
	}
	if !strings.Contains(m.redateCmd, fmt.Sprintf("--seed=%d", m.redateSeed)) {
		t.Errorf("the logged command must carry the new seed %d: %s", m.redateSeed, m.redateCmd)
	}
}

func TestRedateFlow_configuredSeedIsTheStartingSeed(t *testing.T) {
	base := newTestModelN(t, 6)
	cfg := base.cfg
	cfg.Seed = 42
	commits, _ := base.repo.ReadCommits()
	m := resizeTo(New(base.repo, safety.NewManager(base.repo), cfg, base.state, commits), 120, 40)

	if m.redateSeed != 42 {
		t.Errorf("a configured seed should be the starting seed, got %d", m.redateSeed)
	}
}

func TestHelpText_documentsTheReshuffleKey(t *testing.T) {
	if !strings.Contains(helpText(), "reshuffle") {
		t.Error("help should document the r reshuffle key")
	}
}

// TestPickedDays_spreadFieldChangesTheSplit: the strategy pane's Spread field
// re-stages the same days with uneven counts, and the logged command carries it.
func TestPickedDays_spreadFieldChangesTheSplit(t *testing.T) {
	m := pickedDaysOnCalendar(t, newTestModelN(t, 30), 3)
	even := countsOnPickedDays(m)

	m = send(m, "t", "down") // strategy pane, Spread field
	m = send(m, "backspace", "9", "0")

	if !strings.Contains(m.redateCmd, "--spread=90") {
		t.Errorf("the logged command should carry the spread: %s", m.redateCmd)
	}
	if uneven := countsOnPickedDays(m); uneven == even {
		t.Errorf("spread 90 left the split unchanged: %s", uneven)
	}
}

func TestPickedDays_badSpreadShowsAnError(t *testing.T) {
	m := pickedDaysOnCalendar(t, newTestModelN(t, 10), 2)

	m = send(m, "t", "down", "backspace", "1", "5", "0")

	if !strings.Contains(m.activeStrat().form.errMsg, "0 to 100") {
		t.Errorf("spread 150 should show a range error, got %q", m.activeStrat().form.errMsg)
	}
}
