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

func TestToEdit_emptyVariantsReturnsZeroEdit(t *testing.T) {
	r := Result{CommitHash: "abc", Variants: nil}
	e := r.ToEdit(Selection{TitleIdx: 0, BodyIdx: 0, NoBody: false})
	if e.NewSubject != nil {
		t.Fatalf("expected nil NewSubject, got %v", e.NewSubject)
	}
	if e.NewBody != nil {
		t.Fatalf("expected nil NewBody, got %v", e.NewBody)
	}
	if e.DropBody {
		t.Fatal("expected DropBody false")
	}
}

func TestToEdit_outOfRangeIndicesClamped(t *testing.T) {
	r := sampleResult() // 3 variants: indices 0,1,2
	// TitleIdx=99 should clamp to 2 ("Third title"), BodyIdx=99 clamps to 2 ("third body").
	e := r.ToEdit(Selection{TitleIdx: 99, BodyIdx: 99, NoBody: false})
	if e.NewSubject == nil || *e.NewSubject != "Third title" {
		t.Fatalf("expected clamped title 'Third title', got %v", e.NewSubject)
	}
	if e.NewBody == nil || *e.NewBody != "third body" {
		t.Fatalf("expected clamped body 'third body', got %v", e.NewBody)
	}
}
