package proc

import (
	"strings"
	"testing"
)

func TestReadJSONLines(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"ready","engine":"whisper"}`,
		`{"type":"utterance","id":1,"text":"Hello world.","start_ms":1200,"end_ms":3400,"asr_ms":180,"engine":"whisper"}`,
		`{"type":"error","message":"boom"}`,
	}, "\n")

	ch := make(chan HelperEvent, 8)
	readJSONLines(strings.NewReader(input), ch)
	close(ch)

	var got []HelperEvent
	for ev := range ch {
		got = append(got, ev)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d", len(got))
	}
	if got[0].Type != "ready" || got[0].Engine != "whisper" {
		t.Fatalf("unexpected ready event: %+v", got[0])
	}
	if got[1].Type != "utterance" || got[1].ID != 1 || got[1].Text != "Hello world." || got[1].StartMs != 1200 || got[1].EndMs != 3400 || got[1].ASRMs != 180 {
		t.Fatalf("unexpected utterance event: %+v", got[1])
	}
	if got[2].Type != "error" || got[2].Message != "boom" {
		t.Fatalf("unexpected error event: %+v", got[2])
	}
}
