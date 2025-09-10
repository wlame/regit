package ui

import (
	"testing"
	"time"

	"github.com/wlame/regit/internal/model"
)

// modelWithCommits builds a minimal Model with a plan over the given root-first
// commits and a Set selection of the given hashes.
func modelWithSet(commits []model.Commit, selected ...string) Model {
	m := Model{plan: model.NewPlan(commits)}
	m.sel = newSelection()
	for _, h := range selected {
		m.sel.toggle(h, false)
	}
	return m
}

func linearModelCommits(hashes ...string) []model.Commit {
	out := make([]model.Commit, len(hashes))
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for i, h := range hashes {
		out[i] = model.Commit{Hash: h, Subject: "c " + h, OrigAuthorDate: base.Add(time.Duration(i) * time.Minute)}
		if i > 0 {
			out[i].ParentHashes = []string{hashes[i-1]}
		}
	}
	return out
}

func TestAffectedChains_fromSetSelection(t *testing.T) {
	commits := linearModelCommits("a", "b", "c", "d")
	m := modelWithSet(commits, "a", "b", "d") // chain {a,b}, singleton d
	chains, ignored := m.affectedChains()
	if len(chains) != 1 || len(chains[0].Members) != 2 {
		t.Fatalf("want one 2-member chain, got %+v", chains)
	}
	if len(ignored) != 1 || ignored[0] != "d" {
		t.Errorf("want [d] ignored, got %v", ignored)
	}
}

func TestSquashChange_titleAndPreview(t *testing.T) {
	commits := linearModelCommits("a", "b", "c")
	chains, _ := model.DetectChains(commits, map[string]bool{"a": true, "b": true})
	chains[0] = model.DefaultSquashConfig(chains[0])
	chains[0].Subject = "Combined"
	sc := squashChange{commits: commits, plan: model.SquashPlan{Chains: chains}}
	if got := sc.title(); got == "" {
		t.Error("title should be non-empty")
	}
	pm := sc.preview(styles{})
	if len(pm.lines) != 1 {
		t.Fatalf("want 1 preview line, got %d", len(pm.lines))
	}
}
