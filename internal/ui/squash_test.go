package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/squash"
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

func wizardState() squashState {
	commits := linearModelCommits("aaaa", "bbbb", "cccc")
	chains, _ := model.DetectChains(commits, map[string]bool{"aaaa": true, "bbbb": true, "cccc": true})
	chains[0] = model.DefaultSquashConfig(chains[0])
	chains[0].Subject = "Combined subject"
	return squashState{chains: chains, tab: 0, field: fldTitle, loading: map[string]bool{}}
}

func TestRenderSquashWizard_newestFirstAndShowsResult(t *testing.T) {
	st := wizardState()
	out := renderSquashWizard(st, 100, 24, styles{})
	// Newest member (cccc) must render ABOVE the oldest (aaaa).
	iNew := strings.Index(out, "cccc")
	iOld := strings.Index(out, "aaaa")
	if iNew < 0 || iOld < 0 || iNew > iOld {
		t.Errorf("commits should be newest-first (cccc before aaaa); idx new=%d old=%d", iNew, iOld)
	}
	if !strings.Contains(out, "Combined subject") {
		t.Error("result title should be shown")
	}
}

func TestRenderSquashWizard_loadingShowsSpinnerText(t *testing.T) {
	st := wizardState()
	st.loading[st.chains[0].Last().Hash] = true
	out := renderSquashWizard(st, 100, 24, styles{})
	if !strings.Contains(strings.ToLower(out), "summariz") {
		t.Error("a loading chain should show a summarizing indicator")
	}
}

func TestSquashState_switchTabClampsAndCycles(t *testing.T) {
	commits := linearModelCommits("a", "b", "c", "d", "e")
	chains, _ := model.DetectChains(commits, map[string]bool{"a": true, "b": true, "d": true, "e": true})
	st := squashState{chains: chains, loading: map[string]bool{}}
	st = st.switchTab(1)
	if st.tab != 1 {
		t.Errorf("tab = %d, want 1", st.tab)
	}
	st = st.switchTab(1) // past the end wraps to 0
	if st.tab != 0 {
		t.Errorf("tab should wrap to 0, got %d", st.tab)
	}
}

func TestSummarizeResult_appliesUnlessEdited(t *testing.T) {
	st := wizardState()
	key := st.chains[0].Last().Hash
	st.loading[key] = true
	st = st.applySummary(key, squash.Proposal{Subject: "LLM subject", Body: "LLM body"})
	if st.chains[0].Subject != "LLM subject" || st.chains[0].Body != "LLM body" {
		t.Errorf("summary should fill the chain, got %q/%q", st.chains[0].Subject, st.chains[0].Body)
	}
	if st.loading[key] {
		t.Error("loading flag should clear once the summary lands")
	}
}

func TestSquashState_editDateField(t *testing.T) {
	st := wizardState()
	st.dateBuf = []string{"2026-03-09 09:02"}
	st.field = fldDate
	st = editActiveField(st, "5", false)
	if st.dateBuf[0] != "2026-03-09 09:025" {
		t.Fatalf("append failed: %q", st.dateBuf[0])
	}
	st = editActiveField(st, "", true)
	if st.dateBuf[0] != "2026-03-09 09:02" {
		t.Errorf("backspace failed: %q", st.dateBuf[0])
	}
}

func TestRestructureMenu_includesSquash(t *testing.T) {
	feats := featuresFor(WSRestructure)
	found := false
	for _, f := range feats {
		if strings.Contains(strings.ToLower(f.Name), "squash") {
			found = true
		}
	}
	if !found {
		t.Error("the Restructure menu should include a Squash row")
	}
}

func TestStageSquash_rejectsUnparseableDate(t *testing.T) {
	commits := linearModelCommits("a", "b", "c")
	m := modelWithSet(commits, "a", "b")
	chains, _ := m.affectedChains()
	chains[0] = model.DefaultSquashConfig(chains[0])
	m.squashSt = squashState{chains: chains, dateBuf: []string{"not a date"}, loading: map[string]bool{}}
	out, _ := m.stageSquash()
	if mm, ok := out.(Model); ok && mm.mode == modePipeline {
		t.Error("stageSquash must not advance to the pipeline on an unparseable date")
	}
}
