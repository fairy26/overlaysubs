package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fairy26/overlaysubs/internal/config"
	"github.com/fairy26/overlaysubs/internal/models"
	"github.com/fairy26/overlaysubs/internal/pfml"
	"github.com/fairy26/overlaysubs/internal/proc"
	"github.com/fairy26/overlaysubs/internal/session"
	"github.com/fairy26/overlaysubs/internal/translator"
	"github.com/fairy26/overlaysubs/internal/uiapp"
)

func main() {
	hw := models.DetectHardware()
	reg, err := models.LoadBundled()
	if err != nil {
		log.Fatalf("load bundled models.json: %v", err)
	}
	settings, err := config.Load(hw)
	if err != nil {
		log.Printf("warning: failed to load settings, using defaults: %v", err)
	}

	asrFlag := flag.String("asr", "", "ASR engine override: whisper or parakeet")
	translatorFlag := flag.String("translator", "", "Translator model override: 8b, 2b, or model ID")
	appFlag := flag.String("app", "", "Target app bundle ID (e.g. com.google.Chrome; use 'all' for system audio)")
	formFlag := flag.String("form", "", "Japanese translation style: polite or plain")
	topicFlag := flag.String("topic", "", "Optional topic context hint")
	modelsDirFlag := flag.String("models-dir", "", "Directory to store downloaded models")
	saveWavFlag := flag.String("save-wav", "", "Save captured 16kHz mono audio to WAV file")
	inputWavFlag := flag.String("input-wav", "", "Replay WAV file through VAD -> ASR -> Translation")
	autoStartFlag := flag.Bool("autostart", false, "Start subtitle pipeline automatically on GUI launch")
	downloadOnlyFlag := flag.Bool("download-only", false, "Download required models and exit")
	headlessFlag := flag.Bool("headless", false, "Run pipeline without GUI and print captions to stdout")
	flag.Parse()

	if *asrFlag != "" {
		settings.ASREngine = strings.ToLower(*asrFlag)
	}
	if *translatorFlag != "" {
		switch strings.ToLower(*translatorFlag) {
		case "8b":
			settings.TranslatorModelID = models.ModelPlamo8BQ4KM
		case "2b":
			settings.TranslatorModelID = models.ModelPlamo2BQ4KM
		default:
			settings.TranslatorModelID = *translatorFlag
		}
	}
	if *appFlag != "" {
		if strings.EqualFold(*appFlag, "all") || strings.EqualFold(*appFlag, "system") {
			settings.AppBundleID = ""
		} else {
			settings.AppBundleID = *appFlag
		}
	}
	if *formFlag != "" {
		settings.Form = pfml.Form(strings.ToLower(*formFlag))
	}
	if *topicFlag != "" {
		settings.Topic = *topicFlag
	}
	if *modelsDirFlag != "" {
		settings.ModelsDir = *modelsDirFlag
	}
	settings.Normalize(hw)

	if *downloadOnlyFlag {
		if err := downloadAll(context.Background(), reg, settings); err != nil {
			log.Fatalf("download failed: %v", err)
		}
		return
	}

	if *headlessFlag {
		if err := runHeadless(hw, reg, settings, *saveWavFlag, *inputWavFlag); err != nil {
			log.Fatalf("headless pipeline failed: %v", err)
		}
		return
	}

	if err := uiapp.RunGUI(uiapp.RunOptions{
		Settings:    settings,
		Hardware:    hw,
		Registry:    reg,
		AutoStart:   *autoStartFlag,
		SaveWavPath: *saveWavFlag,
		InputWav:    *inputWavFlag,
	}); err != nil {
		log.Fatalf("mygo app failed: %v", err)
	}
}

func downloadAll(ctx context.Context, reg *models.Registry, s config.Settings) error {
	ids := models.RequiredModelIDs(s.ASREngine, s.TranslatorModelID)
	for _, id := range ids {
		m, ok := reg.Get(id)
		if !ok {
			return fmt.Errorf("unknown model id: %s", id)
		}
		fmt.Fprintf(os.Stderr, "downloading %s (%s)...\n", m.ID, m.File)
		path, err := models.Fetch(ctx, m, s.ModelsDir, func(done, total int64) {
			if total > 0 {
				fmt.Fprintf(os.Stderr, "\r  %s: %.1f%% (%.1f / %.1f MB)", m.File, float64(done)*100/float64(total), float64(done)/(1<<20), float64(total)/(1<<20))
			}
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "\n  saved to %s\n", path)
	}
	return nil
}

func runHeadless(hw models.Hardware, reg *models.Registry, s config.Settings, saveWav, inputWav string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	vadPath, err := reg.Path(s.ModelsDir, models.ModelSileroVAD)
	if err != nil {
		return err
	}
	mtPath, err := reg.Path(s.ModelsDir, s.TranslatorModelID)
	if err != nil {
		return err
	}

	helperBin, err := proc.ResolveBinary(
		s.CaptureHelperBin,
		"capture-helper",
		filepath.Join("helper", ".build", "release", "capture-helper"),
		filepath.Join("helper", ".build", "debug", "capture-helper"),
	)
	if err != nil {
		return err
	}

	var whisperSrv *proc.ServerProcess
	var whisperURL string
	if s.ASREngine == models.ASRWhisper {
		asrPath, err := reg.Path(s.ModelsDir, models.ModelWhisperLargeV3Turbo)
		if err != nil {
			return err
		}
		whisperBin, err := proc.ResolveBinary(s.WhisperServerBin, "whisper-server")
		if err != nil {
			return err
		}
		whisperSrv, err = proc.StartWhisperServer(ctx, whisperBin, asrPath, vadPath, hw.CPUCount)
		if err != nil {
			return err
		}
		defer whisperSrv.Stop()
		whisperURL = whisperSrv.BaseURL
	}

	llamaBin, err := proc.ResolveBinary(s.LlamaServerBin, "llama-server")
	if err != nil {
		return err
	}
	llamaSrv, err := proc.StartLlamaServer(ctx, llamaBin, mtPath, hw.CPUCount)
	if err != nil {
		return err
	}
	defer llamaSrv.Stop()

	helper, err := proc.StartHelper(proc.HelperOptions{
		BinaryPath:   helperBin,
		ASREngine:    s.ASREngine,
		WhisperURL:   whisperURL,
		VADModelPath: vadPath,
		AppBundleID:  s.AppBundleID,
		Language:     s.Language,
		SaveWavPath:  saveWav,
		InputWavPath: inputWav,
	})
	if err != nil {
		return err
	}
	defer helper.Stop()

	sess := session.New(session.Config{
		SourceLang: s.Language,
		TargetLang: s.TargetLang,
		Form:       s.Form,
		Topic:      s.Topic,
		Glossary:   s.Glossary,
		UseContext: s.UseContext,
	})
	mtClient := translator.NewLlamaClient(llamaSrv.BaseURL)

	for ev := range helper.Events {
		switch ev.Type {
		case "ready":
			fmt.Fprintf(os.Stderr, "[ready] engine=%s\n", ev.Engine)
		case "error":
			fmt.Fprintf(os.Stderr, "[error] %s\n", ev.Message)
		case "utterance":
			if err := sess.HandleUtterance(ctx, ev.Text, mtClient, func(c session.Caption) {
				if c.Stage == session.StageFinal {
					fmt.Printf("[%d-%dms | asr=%dms] %s\n  -> %s\n", ev.StartMs, ev.EndMs, ev.ASRMs, c.Source, c.Translation)
				}
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
