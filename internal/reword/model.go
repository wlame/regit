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

// ToEdit maps a Selection over this Result onto the existing model.Edit, which
// the rewrite engine consumes. The body is dropped when NoBody is set or the
// chosen body is empty, yielding a one-line commit.
func (r Result) ToEdit(sel Selection) model.Edit {
	title := r.Variants[sel.TitleIdx].Title
	e := model.Edit{NewSubject: &title}
	if sel.NoBody {
		e.DropBody = true
		return e
	}
	body := r.Variants[sel.BodyIdx].Body
	if body == "" {
		e.DropBody = true
		return e
	}
	e.NewBody = &body
	return e
}
