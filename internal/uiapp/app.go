package uiapp

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/fairy26/overlaysubs/internal/config"
	"github.com/fairy26/overlaysubs/internal/models"
	"github.com/fairy26/overlaysubs/internal/proc"
	"github.com/fairy26/overlaysubs/internal/session"
	"github.com/fairy26/overlaysubs/internal/translator"
)

type RunOptions struct {
	Settings    config.Settings
	Hardware    models.Hardware
	Registry    *models.Registry
	AutoStart   bool
	SaveWavPath string
	InputWav    string
}

type Controller struct {
	hw       models.Hardware
	reg      *models.Registry
	settings config.Settings
	saveWav  string
	inputWav string

	controlWin *mygo.Window
	overlayWin *mygo.Window
	overlay    *OverlayState

	statusText       string
	lastError        string
	running          bool
	starting         bool
	downloading      bool
	downloadProgress float64
	downloadLabel    string
	recentCaptions   []session.Caption

	pipeMu        sync.Mutex
	pipeCancel    context.CancelFunc
	whisperSrv    *proc.ServerProcess
	llamaSrv      *proc.ServerProcess
	helperProc    *proc.HelperProcess
	activeSession *session.Session
}

func RunGUI(opts RunOptions) error {
	ctrl := &Controller{
		hw:         opts.Hardware,
		reg:        opts.Registry,
		settings:   opts.Settings,
		saveWav:    opts.SaveWavPath,
		inputWav:   opts.InputWav,
		statusText: "Ready",
		overlay: &OverlayState{
			ShowSource: opts.Settings.ShowSource,
			FontSize:   opts.Settings.OverlayFontSize,
			MaxLines:   opts.Settings.OverlayLines,
			Linger:     time.Duration(opts.Settings.OverlayLingerSec) * time.Second,
		},
	}

	mygo.App.SetName("OverlaySubs")
	mygo.App.WhenReady(func() {
		ctrl.overlayWin = CreateOverlayWindow(ctrl.overlay)
		ctrl.controlWin = mygo.NewWindow(mygo.WindowOptions{
			Title:     "OverlaySubs",
			Width:     620,
			Height:    640,
			MinWidth:  540,
			MinHeight: 500,
			Content:   ui.View(ctrl.controlView),
		})
		ctrl.controlWin.OnClosed(func() {
			ctrl.StopPipeline()
			mygo.App.Quit()
		})

		mygo.Screen.OnDisplaysChanged(func() {
			if ctrl.overlayWin != nil {
				b := OverlayBounds(primaryWorkArea())
				ctrl.overlayWin.SetBounds(b)
			}
		})

		// Periodic timer to expire old captions after silence linger.
		go ctrl.lingerLoop()

		if opts.AutoStart {
			ctrl.StartPipeline()
		}
	})

	mygo.App.OnWillQuit(func(_ *mygo.QuitEvent) {
		ctrl.StopPipeline()
	})

	return mygo.App.Run()
}

func (a *Controller) lingerLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		mygo.RunOnMain(func() {
			if a.overlay != nil && a.overlay.ClearIfExpired(time.Now()) {
				if a.overlayWin != nil {
					a.overlayWin.Invalidate()
				}
			}
		})
	}
}

func (a *Controller) missingModels() []string {
	ids := models.RequiredModelIDs(a.settings.ASREngine, a.settings.TranslatorModelID)
	var missing []string
	for _, id := range ids {
		if !a.reg.IsDownloaded(a.settings.ModelsDir, id) {
			missing = append(missing, id)
		}
	}
	return missing
}

func (a *Controller) saveSettings() {
	if err := config.Save(a.settings); err != nil {
		log.Printf("save settings error: %v", err)
	}
}

func (a *Controller) syncSessionConfig() {
	a.pipeMu.Lock()
	sess := a.activeSession
	a.pipeMu.Unlock()
	if sess == nil {
		return
	}
	sess.UpdateConfig(session.Config{
		SourceLang: a.settings.Language,
		TargetLang: a.settings.TargetLang,
		Form:       a.settings.Form,
		Topic:      a.settings.Topic,
		Glossary:   a.settings.Glossary,
		UseContext: a.settings.UseContext,
	})
}

func (a *Controller) ShowSampleSubtitle() {
	now := time.Now()
	a.overlay.ShowSource = a.settings.ShowSource
	a.overlay.Upsert(session.Caption{
		ID:          999999,
		Stage:       session.StageFinal,
		Lang:        "en",
		Source:      "Welcome back everyone, today we are building a local subtitle overlay.",
		Translation: "皆さんおかえりなさい。今日はローカル字幕オーバーレイを作ります。",
	}, now)
	if a.overlayWin != nil {
		a.overlayWin.Invalidate()
	}
}

func (a *Controller) DownloadRequiredModels() {
	if a.downloading {
		return
	}
	a.downloading = true
	a.lastError = ""
	a.downloadProgress = 0
	a.statusText = "Downloading models..."
	if a.controlWin != nil {
		a.controlWin.Invalidate()
	}

	settingsCopy := a.settings
	go func() {
		ids := models.RequiredModelIDs(settingsCopy.ASREngine, settingsCopy.TranslatorModelID)
		for idx, id := range ids {
			m, ok := a.reg.Get(id)
			if !ok {
				a.setDownloadError(fmt.Errorf("unknown model %s", id))
				return
			}
			_, err := models.Fetch(context.Background(), m, settingsCopy.ModelsDir, func(done, total int64) {
				frac := 0.0
				if total > 0 {
					frac = float64(done) / float64(total)
				}
				label := fmt.Sprintf("[%d/%d] %s (%.1f / %.1f MB)", idx+1, len(ids), m.File, float64(done)/(1<<20), float64(total)/(1<<20))
				a.updateControl(func() {
					a.downloadProgress = frac
					a.downloadLabel = label
				})
			})
			if err != nil {
				a.setDownloadError(err)
				return
			}
		}
		a.updateControl(func() {
			a.downloading = false
			a.downloadProgress = 1
			a.downloadLabel = ""
			a.statusText = "Models ready"
		})
	}()
}

func (a *Controller) setDownloadError(err error) {
	a.updateControl(func() {
		a.downloading = false
		a.statusText = "Download failed"
		a.lastError = err.Error()
	})
}

func (a *Controller) StartPipeline() {
	if a.running || a.starting {
		return
	}
	if missing := a.missingModels(); len(missing) > 0 {
		a.lastError = fmt.Sprintf("Download required models first: %v", missing)
		if a.controlWin != nil {
			a.controlWin.Invalidate()
		}
		return
	}

	a.starting = true
	a.lastError = ""
	a.statusText = "Starting local inference servers..."
	if a.controlWin != nil {
		a.controlWin.Invalidate()
	}

	settingsCopy := a.settings
	ctx, cancel := context.WithCancel(context.Background())
	a.pipeMu.Lock()
	a.pipeCancel = cancel
	a.pipeMu.Unlock()

	go a.runPipeline(ctx, settingsCopy)
}

func (a *Controller) runPipeline(ctx context.Context, s config.Settings) {
	fail := func(err error) {
		log.Printf("pipeline error: %v", err)
		a.cleanupChildren()
		a.updateControl(func() {
			a.starting = false
			a.running = false
			a.statusText = "Stopped (error)"
			a.lastError = err.Error()
		})
	}

	vadPath, err := a.reg.Path(s.ModelsDir, models.ModelSileroVAD)
	if err != nil {
		fail(err)
		return
	}
	mtPath, err := a.reg.Path(s.ModelsDir, s.TranslatorModelID)
	if err != nil {
		fail(err)
		return
	}

	helperBin, err := proc.ResolveBinary(
		s.CaptureHelperBin,
		"capture-helper",
		filepath.Join("helper", ".build", "release", "capture-helper"),
		filepath.Join("helper", ".build", "debug", "capture-helper"),
	)
	if err != nil {
		fail(fmt.Errorf("capture-helper not found (build with `cd helper && swift build -c release`): %w", err))
		return
	}

	llamaBin, err := proc.ResolveBinary(s.LlamaServerBin, "llama-server")
	if err != nil {
		fail(err)
		return
	}

	var whisperSrv *proc.ServerProcess
	var whisperURL string
	if s.ASREngine == models.ASRWhisper {
		a.updateControl(func() { a.statusText = "Starting whisper-server (Metal)..." })
		asrPath, err := a.reg.Path(s.ModelsDir, models.ModelWhisperLargeV3Turbo)
		if err != nil {
			fail(err)
			return
		}
		whisperBin, err := proc.ResolveBinary(s.WhisperServerBin, "whisper-server")
		if err != nil {
			fail(err)
			return
		}
		whisperSrv, err = proc.StartWhisperServer(ctx, whisperBin, asrPath, vadPath, a.hw.CPUCount)
		if err != nil {
			fail(err)
			return
		}
		whisperURL = whisperSrv.BaseURL
		a.pipeMu.Lock()
		a.whisperSrv = whisperSrv
		a.pipeMu.Unlock()
	}

	a.updateControl(func() { a.statusText = "Starting llama-server (PLaMo 3 Translate, Metal)..." })
	llamaSrv, err := proc.StartLlamaServer(ctx, llamaBin, mtPath, a.hw.CPUCount)
	if err != nil {
		fail(err)
		return
	}
	a.pipeMu.Lock()
	a.llamaSrv = llamaSrv
	a.pipeMu.Unlock()

	a.updateControl(func() { a.statusText = "Starting capture-helper..." })
	helper, err := proc.StartHelper(proc.HelperOptions{
		BinaryPath:   helperBin,
		ASREngine:    s.ASREngine,
		WhisperURL:   whisperURL,
		VADModelPath: vadPath,
		AppBundleID:  s.AppBundleID,
		Language:     s.Language,
		SaveWavPath:  a.saveWav,
		InputWavPath: a.inputWav,
	})
	if err != nil {
		fail(err)
		return
	}

	sess := session.New(session.Config{
		SourceLang: s.Language,
		TargetLang: s.TargetLang,
		Form:       s.Form,
		Topic:      s.Topic,
		Glossary:   s.Glossary,
		UseContext: s.UseContext,
	})
	mtClient := translator.NewLlamaClient(llamaSrv.BaseURL)

	a.pipeMu.Lock()
	a.helperProc = helper
	a.activeSession = sess
	a.pipeMu.Unlock()

	a.updateControl(func() {
		a.starting = false
		a.running = true
		a.statusText = fmt.Sprintf("Translating (%s + %s)", s.ASREngine, s.TranslatorModelID)
	})

	for ev := range helper.Events {
		if ctx.Err() != nil {
			break
		}
		switch ev.Type {
		case "ready":
			log.Printf("capture-helper ready (engine=%s)", ev.Engine)
		case "error":
			log.Printf("capture-helper error: %s", ev.Message)
			a.updateControl(func() {
				a.lastError = ev.Message
			})
		case "utterance":
			err := sess.HandleUtterance(ctx, ev.Text, mtClient, func(capItem session.Caption) {
				now := time.Now()
				if a.overlayWin != nil {
					a.overlayWin.Update(func() {
						a.overlay.Upsert(capItem, now)
					})
				}
				if capItem.Stage == session.StageFinal {
					a.updateControl(func() {
						a.recentCaptions = append(a.recentCaptions, capItem)
						if len(a.recentCaptions) > 20 {
							a.recentCaptions = a.recentCaptions[len(a.recentCaptions)-20:]
						}
					})
				}
			})
			if err != nil && ctx.Err() == nil {
				log.Printf("translation error: %v", err)
				a.updateControl(func() {
					a.lastError = err.Error()
				})
			}
		}
	}

	a.cleanupChildren()
	a.updateControl(func() {
		a.starting = false
		a.running = false
		if a.lastError == "" {
			a.statusText = "Stopped"
		}
	})
}

func (a *Controller) StopPipeline() {
	a.pipeMu.Lock()
	cancel := a.pipeCancel
	a.pipeCancel = nil
	a.pipeMu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.cleanupChildren()
	a.updateControl(func() {
		a.starting = false
		a.running = false
		a.statusText = "Stopped"
	})
}

func (a *Controller) cleanupChildren() {
	a.pipeMu.Lock()
	helper := a.helperProc
	whisper := a.whisperSrv
	llama := a.llamaSrv
	a.helperProc = nil
	a.whisperSrv = nil
	a.llamaSrv = nil
	a.activeSession = nil
	a.pipeMu.Unlock()

	helper.Stop()
	whisper.Stop()
	llama.Stop()
}

func (a *Controller) updateControl(fn func()) {
	if a.controlWin != nil {
		a.controlWin.Update(fn)
	} else {
		fn()
	}
}
