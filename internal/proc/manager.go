package proc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// ResolveBinary finds an executable by explicit override, next to the running binary,
// in common local build/Homebrew paths, or on PATH.
func ResolveBinary(override, name string, extraCandidates ...string) (string, error) {
	if override != "" {
		if st, err := os.Stat(override); err == nil && !st.IsDir() {
			return override, nil
		}
		return "", fmt.Errorf("%s binary not found at %s", name, override)
	}

	var candidates []string
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, name),
			filepath.Join(exeDir, "..", "Resources", name),
		)
	}
	candidates = append(candidates, extraCandidates...)
	candidates = append(candidates,
		filepath.Join("/opt/homebrew/bin", name),
		filepath.Join("/usr/local/bin", name),
	)

	for _, c := range candidates {
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%s not found in PATH or /opt/homebrew/bin", name)
}

func FreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

type ServerProcess struct {
	Name    string
	BaseURL string
	cmd     *exec.Cmd
	mu      sync.Mutex
	stopped bool
	done    chan error
}

func StartWhisperServer(ctx context.Context, binPath, modelPath, vadModelPath string, threads int) (*ServerProcess, error) {
	port, err := FreePort()
	if err != nil {
		return nil, err
	}
	if threads <= 0 || threads > 8 {
		threads = min(runtime.NumCPU(), 8)
	}
	args := []string{
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"-m", modelPath,
		"-t", strconv.Itoa(threads),
		"-l", "en",
		"-bo", "1",
		"-mc", "0",
		"-sns",
		"-fa",
	}
	if vadModelPath != "" {
		args = append(args, "--vad", "-vm", vadModelPath)
	}
	return startHTTPServer(ctx, "whisper-server", binPath, args, port, 120*time.Second)
}

func StartLlamaServer(ctx context.Context, binPath, modelPath string, threads int) (*ServerProcess, error) {
	port, err := FreePort()
	if err != nil {
		return nil, err
	}
	if threads <= 0 || threads > 8 {
		threads = min(runtime.NumCPU(), 8)
	}
	// Matches crates/core + src-tauri/src/engine.rs:
	// n_ctx=4096, n_batch=4096, n_gpu_layers=999 (all layers on Metal), --special so <|plamo:tag|> triggers stop.
	args := []string{
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"-m", modelPath,
		"-c", "4096",
		"-b", "4096",
		"-ngl", "999",
		"-t", strconv.Itoa(threads),
		"-np", "1",
		"--special",
	}
	return startHTTPServer(ctx, "llama-server", binPath, args, port, 180*time.Second)
}

func startHTTPServer(ctx context.Context, name, binPath string, args []string, port int, timeout time.Duration) (*ServerProcess, error) {
	cmd := exec.Command(binPath, args...)
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
		return nil, fmt.Errorf("start %s: %w", name, err)
	}

	sp := &ServerProcess{
		Name:    name,
		BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		cmd:     cmd,
		done:    make(chan error, 1),
	}

	go pipeLogs(name, stdout)
	go pipeLogs(name, stderr)
	go func() {
		sp.done <- cmd.Wait()
	}()

	if err := sp.waitHealthy(ctx, timeout); err != nil {
		sp.Stop()
		return nil, fmt.Errorf("%s failed to become ready: %w", name, err)
	}
	return sp, nil
}

func (s *ServerProcess) waitHealthy(ctx context.Context, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	client := &http.Client{Timeout: 2 * time.Second}
	healthURL := s.BaseURL + "/health"

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-s.done:
			if err == nil {
				return fmt.Errorf("process exited prematurely")
			}
			return fmt.Errorf("process exited: %w", err)
		case <-deadline.C:
			return fmt.Errorf("timed out after %s waiting for %s", timeout, healthURL)
		case <-ticker.C:
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
			resp, err := client.Do(req)
			if err != nil {
				continue
			}
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
}

func (s *ServerProcess) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	cmd := s.cmd
	s.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-s.done:
		case <-time.After(1 * time.Second):
		}
	}
}

func pipeLogs(prefix string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 16*1024), 256*1024)
	for scanner.Scan() {
		log.Printf("[%s] %s", prefix, scanner.Text())
	}
}
