package uiapp

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/fairy26/overlaysubs/internal/session"
)

func TestOverlayBoundsBottomCenter(t *testing.T) {
	wa := mygo.Rectangle{X: 0, Y: 37, Width: 1470, Height: 850}
	b := OverlayBounds(wa)
	if b.Width != defaultOverlayMaxWidth {
		t.Fatalf("width: got %d, want %d", b.Width, defaultOverlayMaxWidth)
	}
	wantX := wa.X + (wa.Width-b.Width)/2
	if b.X != wantX {
		t.Fatalf("x: got %d, want %d", b.X, wantX)
	}
	wantY := wa.Y + wa.Height - b.Height - overlayBottomMargin
	if b.Y != wantY {
		t.Fatalf("y: got %d, want %d", b.Y, wantY)
	}
}

func TestOverlayStateRenderHeadless(t *testing.T) {
	st := &OverlayState{
		ShowSource: true,
		FontSize:   26,
		MaxLines:   3,
		Linger:     10 * time.Second,
	}
	now := time.Now()
	st.Upsert(session.Caption{
		ID:          1,
		Stage:       session.StageFinal,
		Lang:        "en",
		Source:      "Hello world.",
		Translation: "こんにちは世界。",
	}, now)

	img := ui.Render(st.View, 1000, 220, 1)
	if img == nil || img.Bounds().Dx() != 1000 || img.Bounds().Dy() != 220 {
		t.Fatalf("unexpected rendered image: %v", img)
	}

	if st.ClearIfExpired(now.Add(5 * time.Second)) {
		t.Fatalf("should not expire before linger")
	}
	if !st.ClearIfExpired(now.Add(11 * time.Second)) {
		t.Fatalf("expected expiration after linger")
	}
}
