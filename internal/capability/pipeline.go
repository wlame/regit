package capability

import (
	"errors"
	"fmt"
	"io"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
)

// CodedError carries a process exit code (per the documented exit codes). main
// maps it via ExitCode; a plain error implies code 1.
type CodedError struct {
	Code int
	Err  error
}

func (e CodedError) Error() string { return e.Err.Error() }
func (e CodedError) Unwrap() error { return e.Err }

func usageErr(format string, a ...any) error   { return CodedError{1, fmt.Errorf(format, a...)} }
func guardErr(format string, a ...any) error   { return CodedError{2, fmt.Errorf(format, a...)} }
func runtimeErr(format string, a ...any) error { return CodedError{3, fmt.Errorf(format, a...)} }

// ExitCode returns the process exit code for an error (0 if nil).
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ce CodedError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return 1
}

// applyGuardErr maps a safety-apply error to the right exit code: a plan that
// no longer matches the branch head (H3) is a guard block (exit 2), anything
// else is a runtime failure (exit 3). Wraps with %w (not %v) so the sentinel
// survives the CodedError wrapping — callers (and tests) can still
// errors.Is(err, safety.ErrHeadMoved) on the value applyOrPreview returns.
func applyGuardErr(err error) error {
	if errors.Is(err, safety.ErrHeadMoved) {
		return guardErr("%w", err)
	}
	return runtimeErr("%w", err)
}

// applyOrPreview is the universal pipeline for plan-based mutating verbs:
// preview by default; --execute applies; --execute --force is required to rewrite
// already-pushed commits; dirty tree / out-of-order always block. A backup ref is
// always created by safety.Apply.
func applyOrPreview(ctx *RunContext, plan *model.Plan, a ParsedArgs, preview func(io.Writer, *model.Plan)) error {
	preview(ctx.Stdout, plan)

	if !a.Bool("execute") {
		fmt.Fprintln(ctx.Stdout, "\n(dry run — nothing applied; pass --execute to apply)")
		return nil
	}

	state, err := ctx.Repo.ReadState()
	if err != nil {
		return runtimeErr("read repository state: %v", err)
	}
	guard := safety.CheckGuards(state, plan)
	if guard.Blocked {
		return guardErr("%s", guard.BlockReason)
	}
	if guard.NeedsConfirmation && !a.Bool("force") {
		return guardErr("%s — re-run with --force to rewrite them", guard.ConfirmReason)
	}
	if len(plan.Commits) == 0 {
		return runtimeErr("nothing to apply: empty plan")
	}

	mgr := safety.NewManager(ctx.Repo)
	res, err := mgr.Apply(plan, state, safety.ApplyOptions{
		Sign:          gitio.DropSignatures,
		UnixTimestamp: ctx.Now.Unix(),
		Confirmed:     a.Bool("force"), // satisfies the pushed-history confirmation
		ExpectedHead:  plan.Commits[len(plan.Commits)-1].Hash,
	})
	if err != nil {
		return applyGuardErr(err)
	}
	fmt.Fprintf(ctx.Stdout, "\nApplied. New HEAD %s (backup %s)\n", Short(res.NewHead), res.BackupRef)
	fmt.Fprintln(ctx.Stdout, res.Verification.String())
	return nil
}
