package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

type ProgressFunc func(done, total int64)

// Fetch downloads a model into dir with Range resume, SHA-256 verification, and atomic rename.
// Mirrors src-tauri/src/download.rs.
func Fetch(ctx context.Context, m *ModelDef, dir string, progress ProgressFunc) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create models dir: %w", err)
	}
	dst := filepath.Join(dir, m.File)
	if info, err := os.Stat(dst); err == nil && !info.IsDir() && info.Size() > 0 {
		if progress != nil {
			progress(m.Size, m.Size)
		}
		return dst, nil
	}

	part := dst + ".part"
	var have int64
	if info, err := os.Stat(part); err == nil {
		have = info.Size()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "overlaysubs")
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", m.File, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return "", fmt.Errorf("download %s: unexpected HTTP status %d", m.File, resp.StatusCode)
	}

	resumed := resp.StatusCode == http.StatusPartialContent
	flags := os.O_CREATE | os.O_WRONLY
	if resumed {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}

	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return "", err
	}

	var done int64
	if resumed {
		done = have
	}
	buf := make([]byte, 1<<20) // 1 MiB
	var lastReported int64
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				_ = f.Close()
				return "", werr
			}
			done += int64(n)
			if progress != nil && (done-lastReported >= 4<<20 || done >= m.Size) {
				lastReported = done
				progress(done, m.Size)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = f.Close()
			return "", readErr
		}
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if progress != nil {
		progress(done, m.Size)
	}

	// Verify full file SHA-256 so resumed downloads are validated end-to-end.
	got, err := fileSHA256(part)
	if err != nil {
		return "", err
	}
	if m.SHA256 != "" && got != m.SHA256 {
		_ = os.Remove(part)
		return "", fmt.Errorf("%s: sha256 mismatch (got %s, want %s), deleted partial file", m.File, got, m.SHA256)
	}
	if err := os.Rename(part, dst); err != nil {
		return "", err
	}
	return dst, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
