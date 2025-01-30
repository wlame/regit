package reword

import "github.com/wlame/regit/internal/model"

// Variant is one model-proposed commit message. Title is a single line; Body
// may be empty or multiline.
type Variant struct {
	Title string
	Body  string
}

// Result is the ranked set of variants for one commit (Variants[0] is the most
// relevant, as ordered by the model).
type Result struct {
	CommitHash string
	Variants   []Variant
}

// Selection is the user's choice for one commit: which variant supplies the
// title, which supplies the body, and whether to drop the body entirely.
type Selection struct {
	TitleIdx int
	BodyIdx  int
	NoBody   bool
}

// DefaultSelection selects the top-ranked variant's title and body.
func DefaultSelection() Selection { return Selection{TitleIdx: 0, BodyIdx: 0, NoBody: false} }

// clampIdx clamps i into [0, n-1]. Returns 0 when n == 0 (caller must guard).
func clampIdx(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

// ToEdit maps a Selection over this Result onto the existing model.Edit, which
// the rewrite engine consumes. The body is dropped when NoBody is set or the
// chosen body is empty, yielding a one-line commit.
//
// If Variants is empty, ToEdit returns a zero Edit (a no-op). Out-of-range
// TitleIdx and BodyIdx are clamped to the last valid index rather than panicking.
func (r Result) ToEdit(sel Selection) model.Edit {
	if len(r.Variants) == 0 {
		return model.Edit{}
	}
	n := len(r.Variants)
	title := r.Variants[clampIdx(sel.TitleIdx, n)].Title
	e := model.Edit{NewSubject: &title}
	if sel.NoBody {
		e.DropBody = true
		return e
	}
	body := r.Variants[clampIdx(sel.BodyIdx, n)].Body
	if body == "" {
		e.DropBody = true
		return e
	}
	e.NewBody = &body
	return e
}
