package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/fairy26/overlaysubs/internal/models"
	"github.com/fairy26/overlaysubs/internal/pfml"
)

const (
	DefaultAppBundleID = "com.google.Chrome"
)

type Settings struct {
	ASREngine         string      `json:"asr_engine"`
	TranslatorModelID string      `json:"translator_model_id"`
	AppBundleID       string      `json:"app_bundle_id"`
	Language          string      `json:"language"`
	TargetLang        string      `json:"target_lang"`
	Form              pfml.Form   `json:"form"`
	Topic             string      `json:"topic"`
	Glossary          []pfml.Pair `json:"glossary"`
	UseContext        bool        `json:"use_context"`
	ShowSource        bool        `json:"show_source"`
	OverlayLines      int         `json:"overlay_lines"`
	OverlayLingerSec  int         `json:"overlay_linger_sec"`
	OverlayFontSize   float32     `json:"overlay_font_size"`
	ModelsDir         string      `json:"models_dir"`
	WhisperServerBin  string      `json:"whisper_server_bin,omitempty"`
	LlamaServerBin    string      `json:"llama_server_bin,omitempty"`
	CaptureHelperBin  string      `json:"capture_helper_bin,omitempty"`
}

func AppSupportDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "overlaysubs"), nil
}

func DefaultModelsDir() string {
	dir, err := AppSupportDir()
	if err != nil {
		return "models"
	}
	return filepath.Join(dir, "models")
}

func DefaultSettings(hw models.Hardware) Settings {
	defs := models.SelectDefaults(hw.MemBytes)
	return Settings{
		ASREngine:         defs.ASREngine,
		TranslatorModelID: defs.TranslatorID,
		AppBundleID:       DefaultAppBundleID,
		Language:          "en",
		TargetLang:        "ja",
		Form:              pfml.FormNone,
		UseContext:        true,
		ShowSource:        true,
		OverlayLines:      3,
		OverlayLingerSec:  10,
		OverlayFontSize:   26,
		ModelsDir:         DefaultModelsDir(),
	}
}

func SettingsPath() (string, error) {
	dir, err := AppSupportDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

func Load(hw models.Hardware) (Settings, error) {
	s := DefaultSettings(hw)
	path, err := SettingsPath()
	if err != nil {
		return s, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return DefaultSettings(hw), err
	}
	s.Normalize(hw)
	return s, nil
}

func Save(s Settings) error {
	path, err := SettingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Settings) Normalize(hw models.Hardware) {
	defs := models.SelectDefaults(hw.MemBytes)
	if s.ASREngine != models.ASRWhisper && s.ASREngine != models.ASRParakeet {
		s.ASREngine = defs.ASREngine
	}
	if s.TranslatorModelID == "" {
		s.TranslatorModelID = defs.TranslatorID
	}
	if s.Language == "" {
		s.Language = "en"
	}
	if s.TargetLang == "" {
		s.TargetLang = "ja"
	}
	if s.Form != pfml.FormNone && s.Form != pfml.FormPlain && s.Form != pfml.FormPolite {
		s.Form = pfml.FormNone
	}
	if s.OverlayLines < 1 || s.OverlayLines > 10 {
		s.OverlayLines = 3
	}
	if s.OverlayLingerSec < 2 || s.OverlayLingerSec > 60 {
		s.OverlayLingerSec = 10
	}
	if s.OverlayFontSize < 14 || s.OverlayFontSize > 64 {
		s.OverlayFontSize = 26
	}
	if s.ModelsDir == "" {
		s.ModelsDir = DefaultModelsDir()
	}
}
