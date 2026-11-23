package uiapp

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/fairy26/overlaysubs/internal/session"
)

const (
	defaultOverlayMaxWidth = 1040
	defaultOverlayHeight   = 220
	overlayBottomMargin    = 28
	overlaySideMargin      = 40
)

// OverlayBounds computes a bottom-center rectangle within the display work area.
func OverlayBounds(workArea mygo.Rectangle) mygo.Rectangle {
	w := workArea.Width - overlaySideMargin*2
	if w > defaultOverlayMaxWidth {
		w = defaultOverlayMaxWidth
	}
	if w < 320 {
		w = max(workArea.Width, 320)
	}
	h := defaultOverlayHeight
	if workArea.Height > 0 && h > workArea.Height/2 {
		h = max(120, workArea.Height/2)
	}
	x := workArea.X + (workArea.Width-w)/2
	y := workArea.Y + workArea.Height - h - overlayBottomMargin
	if y < workArea.Y {
		y = workArea.Y
	}
	return mygo.Rectangle{X: x, Y: y, Width: w, Height: h}
}

type OverlayState struct {
	Captions    []session.Caption
	ShowSource  bool
	FontSize    float32
	MaxLines    int
	Linger      time.Duration
	LastUpdated time.Time
}

func (s *OverlayState) Upsert(c session.Caption, now time.Time) {
	s.LastUpdated = now
	for i := range s.Captions {
		if s.Captions[i].ID == c.ID {
			// Avoid flashing a shorter streaming draft over a readable one.
			if c.Stage == session.StageStreaming && len(c.Translation) < len(s.Captions[i].Translation) {
				return
			}
			s.Captions[i] = c
			return
		}
	}
	s.Captions = append(s.Captions, c)
	limit := max(s.MaxLines, 1)
	if len(s.Captions) > limit {
		s.Captions = s.Captions[len(s.Captions)-limit:]
	}
}

func (s *OverlayState) ClearIfExpired(now time.Time) bool {
	if len(s.Captions) == 0 || s.Linger <= 0 {
		return false
	}
	if now.Sub(s.LastUpdated) > s.Linger {
		s.Captions = nil
		return true
	}
	return false
}

func (s *OverlayState) View(c *ui.Context) {
	// Keep the root completely transparent so only caption plates appear over Chrome/YouTube.
	c.Root().Background(ui.Transparent)

	fontSize := s.FontSize
	if fontSize < 14 {
		fontSize = 26
	}
	srcSize := max(float32(12), fontSize*0.58)

	ui.Column(c).
		Fill().
		Justify(ui.End).
		AlignItems(ui.Center).
		Padding(10, 16).
		Gap(8).
		Children(func() {
			for _, capItem := range s.Captions {
				isStreaming := capItem.Stage == session.StageStreaming
				plate := ui.Column(c).
					Background(ui.RGBA(12, 14, 18, 0.74)).
					Radius(10).
					Padding(8, 16).
					Gap(3).
					AlignItems(ui.Center).
					MaxWidthPercent(96)
				if isStreaming {
					plate = plate.Opacity(0.84)
				}
				plate.Children(func() {
					if s.ShowSource && capItem.Source != "" {
						ui.Text(c, capItem.Source).
							FontSize(srcSize).
							TextColor(ui.RGB(205, 214, 226)).
							TextAlign(ui.Center).
							LineHeight(1.25).
							TextShadow(0, 1, 2, ui.RGBA(0, 0, 0, 0.85))
					}
					if capItem.Translation != "" {
						ui.Text(c, capItem.Translation).
							FontSize(fontSize).
							FontWeight(600).
							TextColor(ui.RGB(255, 255, 255)).
							TextAlign(ui.Center).
							LineHeight(1.32).
							TextShadow(0, 1, 3, ui.RGBA(0, 0, 0, 0.92))
					}
				})
			}
		})
}

func CreateOverlayWindow(state *OverlayState) *mygo.Window {
	workArea := primaryWorkArea()
	bounds := OverlayBounds(workArea)
	w := mygo.NewWindow(mygo.WindowOptions{
		Title:             "OverlaySubs Subtitles",
		X:                 bounds.X,
		Y:                 bounds.Y,
		Width:             bounds.Width,
		Height:            bounds.Height,
		Frameless:         true,
		Transparent:       true,
		AlwaysOnTop:       true,
		IgnoreMouseEvents: true,
		DisableShadow:     true,
		DisableResize:     true,
		SkipTaskbar:       true,
		Content:           ui.View(state.View),
	})
	w.SetVisibleOnAllWorkspaces(true)
	return w
}

func primaryWorkArea() mygo.Rectangle {
	displays := mygo.Screen.Displays()
	for _, d := range displays {
		if d.Primary && d.WorkArea.Width > 0 && d.WorkArea.Height > 0 {
			return d.WorkArea
		}
	}
	if len(displays) > 0 && displays[0].WorkArea.Width > 0 {
		return displays[0].WorkArea
	}
	return mygo.Rectangle{X: 0, Y: 0, Width: 1440, Height: 900}
}
