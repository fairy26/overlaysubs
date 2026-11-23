package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBundledRegistry(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled failed: %v", err)
	}
	for _, id := range []string{
		ModelWhisperLargeV3Turbo,
		ModelPlamo8BQ4KM,
		ModelPlamo2BQ4KM,
		ModelSileroVAD,
	} {
		m, ok := r.Get(id)
		if !ok {
			t.Fatalf("missing expected model %q", id)
		}
		if m.URL == "" || m.File == "" || m.Size <= 0 || len(m.SHA256) != 64 {
			t.Fatalf("invalid model entry %+v", m)
		}
	}
}

func TestSelectDefaultsByMemory(t *testing.T) {
	d8 := SelectDefaults(8 * gib)
	if d8.TranslatorID != ModelPlamo2BQ4KM || d8.ASREngine != ASRParakeet {
		t.Fatalf("8GB defaults mismatch: %+v", d8)
	}

	d16 := SelectDefaults(16 * gib)
	if d16.TranslatorID != ModelPlamo8BQ4KM || d16.ASREngine != ASRParakeet {
		t.Fatalf("16GB defaults mismatch: %+v", d16)
	}

	d32 := SelectDefaults(32 * gib)
	if d32.TranslatorID != ModelPlamo8BQ4KM || d32.ASREngine != ASRParakeet {
		t.Fatalf("32GB defaults mismatch: %+v", d32)
	}
}

func TestFetchResumesAndVerifiesSHA256(t *testing.T) {
	payload := []byte("hello overlaysubs model binary content 1234567890")
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])

	// Serve with standard range support via temp file.
	srcDir := t.TempDir()
	srcFile := filepath.Join(srcDir, "model.bin")
	if err := os.WriteFile(srcFile, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	fileSrv := httptest.NewServer(http.FileServer(http.Dir(srcDir)))
	defer fileSrv.Close()

	dstDir := t.TempDir()
	// Pre-populate a partial file to exercise Range resume.
	if err := os.WriteFile(filepath.Join(dstDir, "model.bin.part"), payload[:10], 0o644); err != nil {
		t.Fatal(err)
	}

	m := &ModelDef{
		ID:     "test-model",
		File:   "model.bin",
		URL:    fileSrv.URL + "/model.bin",
		Size:   int64(len(payload)),
		SHA256: hexSum,
	}
	gotPath, err := Fetch(context.Background(), m, dstDir, nil)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	gotBytes, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBytes) != string(payload) {
		t.Fatalf("content mismatch: got %q, want %q", gotBytes, payload)
	}
}
