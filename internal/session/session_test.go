package session

import (
	"context"
	"reflect"
	"testing"

	"github.com/fairy26/overlaysubs/internal/pfml"
)

type fakeTranslator struct {
	reqs []pfml.TranslateRequest
	outs []string
}

func (f *fakeTranslator) Translate(_ context.Context, req pfml.TranslateRequest, onPiece func(string)) (string, error) {
	f.reqs = append(f.reqs, req)
	out := "訳文"
	if len(f.outs) > 0 {
		out = f.outs[0]
		f.outs = f.outs[1:]
	}
	if onPiece != nil {
		onPiece(out)
	}
	return out, nil
}

func TestSessionPassesRollingHistoryAndFiltersGlossary(t *testing.T) {
	s := New(Config{
		SourceLang: "en",
		TargetLang: "ja",
		Form:       pfml.FormPolite,
		Topic:      "Tech talk",
		Glossary:   []pfml.Pair{{Source: "MyGo", Translation: "MyGo"}, {Source: "Rust", Translation: "Rust"}},
		UseContext: true,
	})
	mt := &fakeTranslator{outs: []string{"こんにちは。", "MyGoは純粋なGoです。", "3番目です。", "4番目です。"}}
	var finals []Caption

	utterances := []string{
		"Thank you for watching!", // hallucination: ignored
		"Hello everyone",
		"MyGo is written in pure Go.",
		"Third sentence here.",
		"Fourth sentence here.",
	}
	for _, u := range utterances {
		if err := s.HandleUtterance(context.Background(), u, mt, func(c Caption) {
			if c.Stage == StageFinal {
				finals = append(finals, c)
			}
		}); err != nil {
			t.Fatal(err)
		}
	}

	if len(finals) != 4 {
		t.Fatalf("expected 4 finals, got %d", len(finals))
	}
	if len(mt.reqs[0].History) != 0 {
		t.Fatalf("first utterance should have empty history, got %+v", mt.reqs[0].History)
	}
	if len(mt.reqs[1].History) != 1 || mt.reqs[1].History[0].Source != "Hello everyone." {
		t.Fatalf("second utterance history mismatch: %+v", mt.reqs[1].History)
	}
	if len(mt.reqs[1].Glossary) != 1 || mt.reqs[1].Glossary[0].Source != "MyGo" {
		t.Fatalf("expected only matching glossary term MyGo, got %+v", mt.reqs[1].Glossary)
	}
	if len(mt.reqs[3].History) != MaxHistory {
		t.Fatalf("expected history capped at %d, got %d", MaxHistory, len(mt.reqs[3].History))
	}
}

func TestSplitSentences(t *testing.T) {
	got := SplitSentences("The tank is already in there. Get ready to go in here. Same way? Okay.")
	want := []string{"The tank is already in there.", "Get ready to go in here.", "Same way? Okay."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
