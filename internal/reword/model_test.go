package reword

import "testing"

func sampleResult() Result {
	return Result{CommitHash: "abc", Variants: []Variant{
		{Title: "First title", Body: "first body"},
		{Title: "Second title", Body: ""},
		{Title: "Third title", Body: "third body"},
	}}
}

func TestToEdit_mixesTitleAndBody(t *testing.T) {
	r := sampleResult()
	e := r.ToEdit(Selection{TitleIdx: 0, BodyIdx: 2, NoBody: false})
	if e.NewSubject == nil || *e.NewSubject != "First title" {
		t.Fatalf("subject = %v", e.NewSubject)
	}
	if e.NewBody == nil || *e.NewBody != "third body" {
		t.Fatalf("body = %v", e.NewBody)
	}
	if e.DropBody {
		t.Fatal("DropBody should be false")
	}
}

func TestToEdit_noBodyDropsBody(t *testing.T) {
	e := sampleResult().ToEdit(Selection{TitleIdx: 0, BodyIdx: 0, NoBody: true})
	if !e.DropBody {
		t.Fatal("expected DropBody true")
	}
	if e.NewBody != nil {
		t.Fatal("NewBody should be nil when dropping body")
	}
}

func TestToEdit_emptyChosenBodyDropsBody(t *testing.T) {
	// Variant 1 has an empty body; selecting it as the body should drop the body.
	e := sampleResult().ToEdit(Selection{TitleIdx: 0, BodyIdx: 1, NoBody: false})
	if !e.DropBody {
		t.Fatal("empty chosen body should drop the body")
	}
}
