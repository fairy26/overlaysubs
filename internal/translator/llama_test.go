package translator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fairy26/overlaysubs/internal/pfml"
)

func TestLlamaClientStreamsAndStripsStopToken(t *testing.T) {
	var gotReq completionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/completion" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		chunks := []completionChunk{
			{Content: "会議は", Stop: false},
			{Content: "来週です。", Stop: false},
			{Content: "<|plamo:tag|>", Stop: true},
		}
		for _, ch := range chunks {
			b, _ := json.Marshal(ch)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	client := NewLlamaClient(srv.URL)
	var streamed strings.Builder
	got, err := client.Translate(context.Background(), pfml.TranslateRequest{
		Text:       "The meeting is next week.",
		SourceLang: "en",
		TargetLang: "ja",
		Form:       pfml.FormPolite,
	}, func(piece string) {
		streamed.WriteString(piece)
	})
	if err != nil {
		t.Fatalf("Translate failed: %v", err)
	}
	if got != "会議は来週です。" {
		t.Fatalf("got %q, want %q", got, "会議は来週です。")
	}
	if streamed.String() != "会議は来週です。" {
		t.Fatalf("streamed %q, want %q", streamed.String(), "会議は来週です。")
	}
	if gotReq.Temperature != 0.0 || gotReq.TopK != 1 || gotReq.NPredict != 256 {
		t.Fatalf("unexpected sampling params: %+v", gotReq)
	}
	if len(gotReq.Stop) != 2 || gotReq.Stop[0] != "<|plamo:tag|>" {
		t.Fatalf("unexpected stop list: %+v", gotReq.Stop)
	}
}
