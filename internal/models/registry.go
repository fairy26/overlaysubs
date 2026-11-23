package models

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fairy26/overlaysubs"
)

const (
	ASRWhisper  = "whisper"
	ASRParakeet = "parakeet"

	ModelWhisperLargeV3Turbo = "whisper-large-v3-turbo"
	ModelPlamo8BQ4KM         = "plamo-3-translate-8b-q4_k_m"
	ModelPlamo2BQ4KM         = "plamo-3-translate-2b-q4_k_m"
	ModelSileroVAD           = "silero-vad-v6.2.0"
)

type Kind string

const (
	KindASR        Kind = "asr"
	KindTranslator Kind = "translator"
	KindVAD        Kind = "vad"
)

type ModelDef struct {
	ID         string   `json:"id"`
	Kind       Kind     `json:"kind"`
	Label      string   `json:"label"`
	URL        string   `json:"url"`
	File       string   `json:"file"`
	Size       int64    `json:"size"`
	SHA256     string   `json:"sha256"`
	DefaultFor []string `json:"default_for"`
}

type Registry struct {
	Models []ModelDef `json:"models"`
}

func LoadBundled() (*Registry, error) {
	return ParseRegistry(overlaysubs.ModelsJSON)
}

func ParseRegistry(data []byte) (*Registry, error) {
	var r Registry
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse models.json: %w", err)
	}
	if len(r.Models) == 0 {
		return nil, fmt.Errorf("models.json contains no models")
	}
	return &r, nil
}

func (r *Registry) Get(id string) (*ModelDef, bool) {
	for i := range r.Models {
		if r.Models[i].ID == id {
			return &r.Models[i], true
		}
	}
	return nil, false
}

func (r *Registry) Path(dir, id string) (string, error) {
	m, ok := r.Get(id)
	if !ok {
		return "", fmt.Errorf("unknown model id: %s", id)
	}
	return filepath.Join(dir, m.File), nil
}

func (r *Registry) IsDownloaded(dir, id string) bool {
	p, err := r.Path(dir, id)
	if err != nil {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && !info.IsDir() && info.Size() > 0
}

// RequiredModelIDs returns the model IDs required for the chosen ASR engine and translator model.
func RequiredModelIDs(asrEngine, translatorID string) []string {
	ids := []string{ModelSileroVAD}
	if asrEngine == ASRWhisper {
		ids = append(ids, ModelWhisperLargeV3Turbo)
	}
	if translatorID != "" {
		ids = append(ids, translatorID)
	}
	return ids
}
