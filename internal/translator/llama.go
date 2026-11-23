package translator

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fairy26/overlaysubs/internal/pfml"
)

const (
	DefaultMaxNewTokens = 256
)

// LlamaClient calls a local llama.cpp `llama-server` (`POST /completion`) for PLaMo 3 Translate.
type LlamaClient struct {
	BaseURL    string
	HTTPClient *http.Client
	MaxTokens  int
}

func NewLlamaClient(baseURL string) *LlamaClient {
	return &LlamaClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		MaxTokens: DefaultMaxNewTokens,
	}
}

type completionRequest struct {
	Prompt        string   `json:"prompt"`
	NPredict      int      `json:"n_predict"`
	Temperature   float64  `json:"temperature"`
	TopK          int      `json:"top_k"`
	RepeatPenalty float64  `json:"repeat_penalty"`
	Stop          []string `json:"stop"`
	Stream        bool     `json:"stream"`
	CachePrompt   bool     `json:"cache_prompt"`
}

type completionChunk struct {
	Content string `json:"content"`
	Stop    bool   `json:"stop"`
}

func (c *LlamaClient) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("llama-server health HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *LlamaClient) Translate(ctx context.Context, req pfml.TranslateRequest, onPiece func(piece string)) (string, error) {
	maxTokens := c.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxNewTokens
	}
	payload := completionRequest{
		Prompt:        pfml.Build(req),
		NPredict:      maxTokens,
		Temperature:   0.0,
		TopK:          1,
		RepeatPenalty: 1.0,
		Stop:          pfml.StopStrings,
		Stream:        true,
		CachePrompt:   true,
	}
	rawBody, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/completion", bytes.NewReader(rawBody))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("llama-server completion request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("llama-server HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Handle non-SSE JSON fallback if a server responds with application/json.
	if strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		var res completionChunk
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			return "", fmt.Errorf("decode completion response: %w", err)
		}
		out := strings.TrimSpace(pfml.TrimStop(res.Content))
		if onPiece != nil && out != "" {
			onPiece(out)
		}
		return out, nil
	}

	var full strings.Builder
	emitted := 0
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			break
		}
		var chunk completionChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Content != "" {
			full.WriteString(chunk.Content)
			cur := full.String()
			trimmed := pfml.TrimStop(cur)
			if len(trimmed) < len(cur) {
				// Stop token appeared in stream; emit any remaining valid prefix and finish.
				if onPiece != nil && len(trimmed) > emitted {
					onPiece(trimmed[emitted:])
				}
				return strings.TrimSpace(trimmed), nil
			}
			// Hold back a potential partial `<|plamo:...` tag so it doesn't flash in the overlay.
			safeEnd := len(cur)
			if idx := strings.LastIndex(cur, "<"); idx >= emitted && strings.HasPrefix("<|plamo:", cur[idx:]) {
				safeEnd = idx
			}
			if onPiece != nil && safeEnd > emitted {
				onPiece(cur[emitted:safeEnd])
				emitted = safeEnd
			}
		}
		if chunk.Stop {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read completion stream: %w", err)
	}

	out := pfml.TrimStop(full.String())
	if onPiece != nil && len(out) > emitted {
		onPiece(out[emitted:])
	}
	return strings.TrimSpace(out), nil
}
