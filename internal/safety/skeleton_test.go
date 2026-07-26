package safety

import (
	"errors"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
)

// errInjected marks a deliberately injected failure so a test can tell its own
// fault apart from a real error escaping the skeleton.
var errInjected = errors.New("injected failure")

// TestRunApply_everyFailureStageLeavesTheRepositoryUnchanged is the core
// abort-safety proof. safety's tests are in-package, so they can drive runApply
// with a synthetic applyOp that fails at a chosen stage — no production seam and
// no test-only indirection required. Since Apply, ApplySplit, ApplySquash,
// ApplyLinearize, ApplyPurge, and ApplyRedact all funnel through this skeleton,
// one table here covers the abort behavior of all six.
//
// The tolerated residue is a backup ref: stages after CreateBackupRef leave one
// behind, which safety.runApply documents as expected and harmless.
func TestRunApply_everyFailureStageLeavesTheRepositoryUnchanged(t *testing.T) {
	cases := []struct {
		name string
		op   func(head string) applyOp
	}{
		{
			name: "guards reject",
			op: func(string) applyOp {
				return applyOp{
					Guards: func() error { return errInjected },
					Rewrite: func(string) (rewriteResult, error) {
						t.Fatal("rewrite ran after guards failed")
						return rewriteResult{}, nil
					},
				}
			},
		},
		{
			name: "rewrite fails",
			op: func(string) applyOp {
				return applyOp{
					Guards:  func() error { return nil },
					Rewrite: func(string) (rewriteResult, error) { return rewriteResult{}, errInjected },
				}
			},
		},
		{
			name: "content verification fails",
			op: func(head string) applyOp {
				return applyOp{
					Guards: func() error { return nil },
					// Promise that two DIFFERENT commits are byte-identical. They are
					// not, so verify aborts before the branch ref moves.
					Rewrite: func(string) (rewriteResult, error) {
						return rewriteResult{
							NewHead:      head,
							Expectations: []model.Expectation{{OldCommit: head, NewCommit: head + "~1"}},
							OldLen:       1, NewLen: 1,
						}, nil
					},
				}
			},
		},
		{
			name: "chain structure fails",
			op: func(head string) applyOp {
				return applyOp{
					Guards: func() error { return nil },
					// Claim the new chain holds 99 commits when it holds far fewer.
					Rewrite: func(string) (rewriteResult, error) {
						return rewriteResult{NewHead: head, OldLen: 1, NewLen: 99}, nil
					},
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// newLinearRepo is squash_test.go's existing fixture builder; reuse it
			// rather than adding a third way to make a linear repo in this package.
			repo := newLinearRepo(t, 3)
			head, err := repo.ResolveRef("HEAD")
			if err != nil {
				t.Fatal(err)
			}
			// Repo.Root() is the absolute worktree path, so no fixture needs its
			// signature changed to feed the oracle.
			before := gittest.Snap(t, repo.Root())

			_, err = NewManager(repo).runApply(tc.op(head), ApplyOptions{
				UnixTimestamp: 1, ExpectedHead: head,
			})
			if err == nil {
				t.Fatal("runApply succeeded, want failure")
			}

			gittest.AssertAbortedCleanly(t, before, gittest.Snap(t, repo.Root()))
		})
	}
}

// TestRunApply_abortAfterBackupLeavesOnlyTheBackupRef pins the precise residue
// contract: a failure downstream of CreateBackupRef leaves exactly one new
// backup ref and nothing else — not a moved branch, not a touched index.
func TestRunApply_abortAfterBackupLeavesOnlyTheBackupRef(t *testing.T) {
	repo := newLinearRepo(t, 3)
	head, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	before := gittest.Snap(t, repo.Root())

	_, err = NewManager(repo).runApply(applyOp{
		Guards:  func() error { return nil },
		Rewrite: func(string) (rewriteResult, error) { return rewriteResult{}, errInjected },
	}, ApplyOptions{UnixTimestamp: 7, ExpectedHead: head})
	if err == nil {
		t.Fatal("runApply succeeded, want failure")
	}

	after := gittest.Snap(t, repo.Root())
	var backups int
	for _, c := range before.Diff(after).Changes {
		if c.Kind == "ref" && c.Op == "added" && strings.HasPrefix(c.Path, "refs/regit/backup/") {
			backups++
			continue
		}
		t.Errorf("unexpected change after aborted apply: %s", c)
	}
	if backups != 1 {
		t.Errorf("backup refs added = %d, want exactly 1", backups)
	}
	if after.Refs[before.Head] != before.Refs[before.Head] {
		t.Errorf("branch %s moved during an aborted apply", before.Head)
	}
}

// TestRunApply_syncFailureSurfacesAsWarningNotError pins the documented
// non-fatal contract for op.Sync (see runApply's doc comment): by the time
// Sync runs, the branch has already moved and history is already correct, so
// a failure to refresh the checkout must NOT be reported as an apply failure —
// it must come back as ApplyResult.SyncWarning, naming the fix-up command,
// with a nil error. ApplyPurge and ApplyRedact are the only two real ops that
// set Sync; this drives the skeleton directly with a synthetic one so the
// contract is proven independent of either operation's own plumbing.
func TestRunApply_syncFailureSurfacesAsWarningNotError(t *testing.T) {
	repo := newLinearRepo(t, 3)
	head, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}

	res, err := NewManager(repo).runApply(applyOp{
		Guards: func() error { return nil },
		// Identity "rewrite": NewHead == oldHead, so verify (empty expectations,
		// identical trees) and verifyChainStructure (identical 1-commit chains)
		// both pass trivially, letting the branch move so Sync actually runs.
		Rewrite: func(head string) (rewriteResult, error) {
			return rewriteResult{NewHead: head, OldLen: 1, NewLen: 1}, nil
		},
		Sync: func(string) error { return errInjected },
	}, ApplyOptions{UnixTimestamp: 9, ExpectedHead: head})
	if err != nil {
		t.Fatalf("a Sync failure must not fail the apply, got error: %v", err)
	}
	if res.SyncWarning == "" {
		t.Error("a Sync failure should have produced a non-empty SyncWarning")
	}
	if !strings.Contains(res.SyncWarning, errInjected.Error()) {
		t.Errorf("SyncWarning = %q, want it to name the underlying failure", res.SyncWarning)
	}
}
