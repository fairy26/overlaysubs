package pfml

import (
	"strings"
	"testing"
)

func TestMatchesModelCardExample(t *testing.T) {
	got := Build(TranslateRequest{
		Text:       "会議は来週の月曜日に変更されました。",
		TargetLang: "en",
	})
	want := "<|plamo:tag|>dataset<|plamo:msg|>translation<|plamo:tag|>input<|plamo:msg|>会議は来週の月曜日に変更されました。<|plamo:tag|>output<|plamo:key|>lang<|plamo:val|>en<|plamo:msg|>"
	if got != want {
		t.Fatalf("mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestOrdersContextHistoryGlossaryInput(t *testing.T) {
	got := Build(TranslateRequest{
		Text:       "b",
		SourceLang: "en",
		TargetLang: "ja",
		Form:       FormPolite,
		Context:    "ctx",
		History:    []Pair{{Source: "a", Translation: "A"}},
		Glossary:   []Pair{{Source: "Foo", Translation: "フー"}},
	})
	at := func(sub string) int {
		i := strings.Index(got, sub)
		if i < 0 {
			t.Fatalf("missing %q in %q", sub, got)
		}
		return i
	}
	if !(at("context") < at("<|plamo:msg|>a") && at("<|plamo:msg|>a") < at("glossary") && at("glossary") < at("<|plamo:msg|>b")) {
		t.Fatalf("wrong section ordering: %s", got)
	}
	if !strings.HasSuffix(got, "<|plamo:key|>form<|plamo:val|>polite<|plamo:msg|>") {
		t.Fatalf("missing polite form suffix: %s", got)
	}
	if !strings.Contains(got, "<|plamo:index|>Foo<|plamo:item|>フー") {
		t.Fatalf("missing glossary format: %s", got)
	}
}
