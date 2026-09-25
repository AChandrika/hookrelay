package diagnosis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to a local Ollama server. No API key and no per-token cost:
// the trade-off is speed and quality, which the evals measure.
type Client struct {
	BaseURL string // e.g. http://127.0.0.1:11434
	Model   string // e.g. qwen3:4b
	HTTP    *http.Client
}

func NewClient(baseURL, model string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   model,
		HTTP:    &http.Client{Timeout: 3 * time.Minute}, // CPU inference can be slow
	}
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
	Duration         time.Duration
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string          `json:"model"`
	Messages []chatMessage   `json:"messages"`
	Stream   bool            `json:"stream"`
	Format   json.RawMessage `json:"format"`
	Think    bool            `json:"think"`
	Options  map[string]any  `json:"options"`
}

type chatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Error           string `json:"error"`
}

// Check confirms Ollama is reachable and the model has been pulled.
func (c *Client) Check(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("ollama not reachable at %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return fmt.Errorf("unexpected response from ollama: %w", err)
	}
	for _, m := range tags.Models {
		if m.Name == c.Model || m.Name == c.Model+":latest" {
			return nil
		}
	}
	return fmt.Errorf("model %q is not pulled; run: docker compose exec ollama ollama pull %s", c.Model, c.Model)
}

// Diagnose asks the model for a diagnosis. If the output fails validation,
// it tells the model what was wrong and gives it one more try.
func (c *Client) Diagnose(ctx context.Context, in Input) (Result, Usage, error) {
	msgs := []chatMessage{
		{Role: "system", Content: SystemPrompt()},
		{Role: "user", Content: RenderInput(in)},
	}
	var total Usage
	var lastErr error

	for try := 0; try < 2; try++ {
		start := time.Now()
		content, resp, err := c.chat(ctx, msgs)
		total.Duration += time.Since(start)
		total.PromptTokens += resp.PromptEvalCount
		total.CompletionTokens += resp.EvalCount
		if err != nil {
			return Result{}, total, err
		}

		var r Result
		if err := json.Unmarshal([]byte(content), &r); err != nil {
			lastErr = fmt.Errorf("not valid JSON: %w", err)
		} else if err := r.Validate(); err != nil {
			lastErr = err
		} else {
			return r, total, nil
		}
		msgs = append(msgs,
			chatMessage{Role: "assistant", Content: content},
			chatMessage{Role: "user", Content: "That reply was invalid: " + lastErr.Error() + ". Reply again with JSON that follows the schema exactly."},
		)
	}
	return Result{}, total, fmt.Errorf("model output still invalid after a retry: %w", lastErr)
}

func (c *Client) chat(ctx context.Context, msgs []chatMessage) (string, chatResponse, error) {
	body, err := json.Marshal(chatRequest{
		Model:    c.Model,
		Messages: msgs,
		Stream:   false,
		Format:   responseSchema(),
		Think:    false, // skip "thinking" output on reasoning models like qwen3; it's slow on CPU
		Options:  map[string]any{"temperature": 0, "seed": 42, "num_ctx": 4096},
	})
	if err != nil {
		return "", chatResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", chatResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", chatResponse{}, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", chatResponse{}, err
	}

	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", chatResponse{}, fmt.Errorf("ollama returned HTTP %d with a non-JSON body", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || out.Error != "" {
		return "", out, errors.New("ollama error: " + strings.TrimSpace(out.Error))
	}
	return out.Message.Content, out, nil
}
