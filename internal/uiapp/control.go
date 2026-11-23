package uiapp

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/fairy26/overlaysubs/internal/models"
	"github.com/fairy26/overlaysubs/internal/pfml"
)

var (
	translatorLabels = []string{
		"PLaMo 3 Translate 8B (Q4_K_M)",
		"PLaMo 3 Translate 2B (Q4_K_M)",
	}
	asrLabels = []string{
		"whisper (Whisper large-v3-turbo)",
		"parakeet (Parakeet TDT v3)",
	}
	formLabels = []string{
		"Auto",
		"Polite (敬体)",
		"Plain (常体)",
	}
	appPresets = []string{
		"Google Chrome (com.google.Chrome)",
		"System Audio (All Apps)",
		"Custom Bundle ID",
	}
)

func translatorIDFromLabel(label string) string {
	if strings.Contains(label, "2B") {
		return models.ModelPlamo2BQ4KM
	}
	return models.ModelPlamo8BQ4KM
}

func labelFromTranslatorID(id string) string {
	if id == models.ModelPlamo2BQ4KM {
		return translatorLabels[1]
	}
	return translatorLabels[0]
}

func asrEngineFromLabel(label string) string {
	if strings.HasPrefix(label, "parakeet") {
		return models.ASRParakeet
	}
	return models.ASRWhisper
}

func labelFromASREngine(engine string) string {
	if engine == models.ASRParakeet {
		return asrLabels[1]
	}
	return asrLabels[0]
}

func formFromLabel(label string) pfml.Form {
	switch {
	case strings.HasPrefix(label, "Polite"):
		return pfml.FormPolite
	case strings.HasPrefix(label, "Plain"):
		return pfml.FormPlain
	default:
		return pfml.FormNone
	}
}

func labelFromForm(f pfml.Form) string {
	switch f {
	case pfml.FormPolite:
		return formLabels[1]
	case pfml.FormPlain:
		return formLabels[2]
	default:
		return formLabels[0]
	}
}

func appPresetFromBundleID(bid string) string {
	switch bid {
	case "com.google.Chrome":
		return appPresets[0]
	case "":
		return appPresets[1]
	default:
		return appPresets[2]
	}
}

func (a *Controller) controlView(c *ui.Context) {
	theme := c.Theme()

	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().Padding(20).Gap(14).Children(func() {
			// Header
			ui.Row(c).FillWidth().Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					ui.Text(c, "OverlaySubs").FontSize(22).Bold()
					ui.Textf(c, "%s / %d GiB RAM — Local real-time English to Japanese subtitles", a.hw.Chip, a.hw.MemGiB).
						FontSize(12).TextColor(theme.TextMuted)
				})
				ui.Row(c).Gap(8).Children(func() {
					if a.running {
						if ui.Button(c, "Stop").Clicked() {
							a.StopPipeline()
						}
					} else {
						btn := ui.PrimaryButton(c, "Start Subtitles")
						if a.starting || a.downloading {
							btn = btn.Disabled(true)
						}
						if btn.Clicked() {
							a.StartPipeline()
						}
					}
					if ui.Button(c, "Preview").Clicked() {
						a.ShowSampleSubtitle()
					}
				})
			})

			// Status bar
			statusBg := theme.Surface
			if a.lastError != "" {
				statusBg = theme.Danger.Alpha(0.18)
			} else if a.running {
				statusBg = theme.Success.Alpha(0.16)
			}
			ui.Column(c).FillWidth().Background(statusBg).Radius(8).Padding(10, 12).Gap(4).Children(func() {
				ui.Text(c, a.statusText).FontSize(13).FontWeight(600)
				if a.lastError != "" {
					ui.Text(c, a.lastError).FontSize(12).TextColor(theme.Danger)
				}
				if a.downloading {
					ui.Progress(c, a.downloadProgress).FillWidth()
					ui.Text(c, a.downloadLabel).FontSize(11).TextColor(theme.TextMuted)
				}
			})

			// Models & ASR section
			ui.Column(c).FillWidth().Background(theme.Surface).Radius(10).Padding(14).Gap(10).Children(func() {
				ui.Text(c, "Models & Speech Recognition").FontSize(14).Bold()

				ui.Row(c).FillWidth().Justify(ui.SpaceBetween).AlignItems(ui.Center).Gap(12).Children(func() {
					ui.Text(c, "Translation Model").FontSize(13)
					trChoice := labelFromTranslatorID(a.settings.TranslatorModelID)
					sel := ui.Select(c, &trChoice, translatorLabels).Width(280)
					if a.running || a.starting {
						sel = sel.Disabled(true)
					}
					if sel.Changed() {
						a.settings.TranslatorModelID = translatorIDFromLabel(trChoice)
						a.saveSettings()
					}
				})

				ui.Row(c).FillWidth().Justify(ui.SpaceBetween).AlignItems(ui.Center).Gap(12).Children(func() {
					ui.Text(c, "Speech Recognition (ASR)").FontSize(13)
					asrChoice := labelFromASREngine(a.settings.ASREngine)
					sel := ui.Select(c, &asrChoice, asrLabels).Width(280)
					if a.running || a.starting {
						sel = sel.Disabled(true)
					}
					if sel.Changed() {
						a.settings.ASREngine = asrEngineFromLabel(asrChoice)
						a.saveSettings()
					}
				})

				missing := a.missingModels()
				ui.Row(c).FillWidth().Justify(ui.SpaceBetween).AlignItems(ui.Center).Gap(12).Children(func() {
					if len(missing) == 0 {
						ui.Text(c, "All required models are downloaded.").FontSize(12).TextColor(theme.Success)
					} else {
						ui.Textf(c, "Missing models: %s", strings.Join(missing, ", ")).FontSize(12).TextColor(theme.Warning)
					}
					dlBtn := ui.Button(c, "Download Models")
					if a.downloading || len(missing) == 0 {
						dlBtn = dlBtn.Disabled(true)
					}
					if dlBtn.Clicked() {
						a.DownloadRequiredModels()
					}
				})
			})

			// Audio & Translation preferences
			ui.Column(c).FillWidth().Background(theme.Surface).Radius(10).Padding(14).Gap(10).Children(func() {
				ui.Text(c, "Audio Source & Translation").FontSize(14).Bold()

				ui.Row(c).FillWidth().Justify(ui.SpaceBetween).AlignItems(ui.Center).Gap(12).Children(func() {
					ui.Text(c, "Audio Capture Target").FontSize(13)
					preset := appPresetFromBundleID(a.settings.AppBundleID)
					sel := ui.Select(c, &preset, appPresets).Width(280)
					if a.running || a.starting {
						sel = sel.Disabled(true)
					}
					if sel.Changed() {
						switch preset {
						case appPresets[0]:
							a.settings.AppBundleID = "com.google.Chrome"
						case appPresets[1]:
							a.settings.AppBundleID = ""
						case appPresets[2]:
							if a.settings.AppBundleID == "" || a.settings.AppBundleID == "com.google.Chrome" {
								a.settings.AppBundleID = "com.apple.Safari"
							}
						}
						a.saveSettings()
					}
				})

				if appPresetFromBundleID(a.settings.AppBundleID) == appPresets[2] {
					ui.Row(c).FillWidth().Justify(ui.SpaceBetween).AlignItems(ui.Center).Gap(12).Children(func() {
						ui.Text(c, "Bundle ID").FontSize(13)
						inp := ui.TextInput(c, &a.settings.AppBundleID).Width(280)
						if a.running || a.starting {
							inp = inp.Disabled(true)
						}
						if inp.Changed() {
							a.saveSettings()
						}
					})
				}

				ui.Row(c).FillWidth().Justify(ui.SpaceBetween).AlignItems(ui.Center).Gap(12).Children(func() {
					ui.Text(c, "Japanese Style").FontSize(13)
					formChoice := labelFromForm(a.settings.Form)
					if ui.Select(c, &formChoice, formLabels).Width(280).Changed() {
						a.settings.Form = formFromLabel(formChoice)
						a.syncSessionConfig()
						a.saveSettings()
					}
				})

				ui.Row(c).FillWidth().Justify(ui.SpaceBetween).AlignItems(ui.Center).Gap(12).Children(func() {
					ui.Text(c, "Topic Context").FontSize(13)
					if ui.TextInput(c, &a.settings.Topic).Width(280).Changed() {
						a.syncSessionConfig()
						a.saveSettings()
					}
				})

				ui.Row(c).FillWidth().Justify(ui.SpaceBetween).AlignItems(ui.Center).Gap(12).Children(func() {
					ui.Text(c, "Show Original English Text").FontSize(13)
					if ui.Switch(c, &a.settings.ShowSource).Changed() {
						a.overlay.ShowSource = a.settings.ShowSource
						if a.overlayWin != nil {
							a.overlayWin.Invalidate()
						}
						a.saveSettings()
					}
				})
			})

			// Recent captions log
			ui.Column(c).FillWidth().Background(theme.Surface).Radius(10).Padding(14).Gap(6).Children(func() {
				ui.Text(c, "Recent Subtitles").FontSize(14).Bold()
				if len(a.recentCaptions) == 0 {
					ui.Text(c, "No subtitles yet. Start subtitles and play an English video in Chrome.").
						FontSize(12).TextColor(theme.TextMuted)
				} else {
					for i := len(a.recentCaptions) - 1; i >= 0; i-- {
						item := a.recentCaptions[i]
						ui.Column(c).FillWidth().Padding(4, 0).Gap(1).Children(func() {
							ui.Text(c, fmt.Sprintf("EN: %s", item.Source)).FontSize(11).TextColor(theme.TextMuted)
							ui.Text(c, item.Translation).FontSize(13)
						})
					}
				}
			})
		})
	})
}
