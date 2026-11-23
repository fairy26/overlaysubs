package proc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type HelperEvent struct {
	Type    string `json:"type"`
	ID      int    `json:"id,omitempty"`
	Text    string `json:"text,omitempty"`
	StartMs int    `json:"start_ms,omitempty"`
	EndMs   int    `json:"end_ms,omitempty"`
	ASRMs   int    `json:"asr_ms,omitempty"`
	Engine  string `json:"engine,omitempty"`
	Message string `json:"message,omitempty"`
}

type HelperOptions struct {
	BinaryPath   string
	ASREngine    string
	WhisperURL   string
	VADModelPath string
	AppBundleID  string
	Language     string
	SaveWavPath  string
	InputWavPath string
}

type HelperProcess struct {
	Events <-chan HelperEvent
	Done   <-chan error

	cmd     *exec.Cmd
	mu      sync.Mutex
	stopped bool
	waitCh  chan error
}

func ParseHelperLine(line []byte) (HelperEvent, error) {
	var ev HelperEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return HelperEvent{}, err
	}
	if ev.Type == "" {
		return HelperEvent{}, fmt.Errorf("helper event missing type")
	}
	return ev, nil
}

func StartHelper(opts HelperOptions) (*HelperProcess, error) {
	args := []string{
		"--asr", opts.ASREngine,
		"--language", nonEmpty(opts.Language, "en"),
	}
	if opts.WhisperURL != "" {
		args = append(args, "--whisper-url", opts.WhisperURL)
	}
	if opts.VADModelPath != "" {
		args = append(args, "--vad-model", opts.VADModelPath)
	}
	if opts.AppBundleID != "" {
		args = append(args, "--app", opts.AppBundleID)
	}
	if opts.SaveWavPath != "" {
		args = append(args, "--save-wav", opts.SaveWavPath)
	}
	if opts.InputWavPath != "" {
		args = append(args, "--input-wav", opts.InputWavPath)
	}

	cmd := exec.Command(opts.BinaryPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start capture-helper: %w", err)
	}

	events := make(chan HelperEvent, 32)
	done := make(chan error, 1)
	waitCh := make(chan error, 1)

	go pipeLogs("capture-helper", stderr)
	go func() {
		defer close(events)
		readJSONLines(stdout, events)
		err := cmd.Wait()
		waitCh <- err
		done <- err
		close(done)
	}()

	return &HelperProcess{
		Events: events,
		Done:   done,
		cmd:    cmd,
		waitCh: waitCh,
	}, nil
}

func readJSONLines(r io.Reader, out chan<- HelperEvent) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		ev, err := ParseHelperLine([]byte(line))
		if err != nil {
			out <- HelperEvent{Type: "error", Message: fmt.Sprintf("invalid helper json: %v", err)}
			continue
		}
		out <- ev
	}
}

func (h *HelperProcess) Stop() {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return
	}
	h.stopped = true
	cmd := h.cmd
	h.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-h.waitCh:
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-h.waitCh:
		case <-time.After(1 * time.Second):
		}
	}
}

type AppEntry struct {
	BundleID string `json:"bundle_id"`
	Name     string `json:"name"`
}

func ListApps(ctx context.Context, binaryPath string) ([]AppEntry, error) {
	cmd := exec.CommandContext(ctx, binaryPath, "--list-apps")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var apps []AppEntry
	if err := json.Unmarshal(out, &apps); err != nil {
		return nil, err
	}
	return apps, nil
}

func nonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
