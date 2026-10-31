package serverapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var osHostname = os.Hostname

const apiPrefix = "/api/v1"

// Error is the server's stable error contract: {"error":{"code","message"}}.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("server %d %s: %s", e.Status, e.Code, e.Message)
}

func IsStatus(err error, status int) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.Status == status
}

type Client struct {
	base    *url.URL
	key     string
	version string
	http    *http.Client
}

func New(baseURL, key, version string) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return nil, errors.New("invalid server URL")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	parsed.RawQuery, parsed.Fragment = "", ""
	if strings.HasSuffix(parsed.Path, "/api/v1") {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/api/v1")
	}
	return &Client{
		base:    parsed,
		key:     key,
		version: version,
		http:    &http.Client{Timeout: 90 * time.Second},
	}, nil
}

func (c *Client) endpoint(path string) string {
	return c.base.String() + apiPrefix + path
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), reader)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+c.key)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "wzstudy-agent/"+c.version)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return response.StatusCode, err
	}
	if response.StatusCode == http.StatusNoContent {
		return response.StatusCode, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &envelope)
		return response.StatusCode, &Error{Status: response.StatusCode, Code: envelope.Error.Code, Message: envelope.Error.Message}
	}
	if out == nil {
		return response.StatusCode, nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return response.StatusCode, fmt.Errorf("decode response: %w", err)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return response.StatusCode, fmt.Errorf("decode data: %w", err)
	}
	return response.StatusCode, nil
}

func (c *Client) UploadAsset(ctx context.Context, path string) (Asset, error) {
	file, err := os.Open(path)
	if err != nil {
		return Asset{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return Asset{}, errors.New("asset must be a regular file")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", filepath.Base(path))
	if err != nil {
		return Asset{}, err
	}
	if _, err := io.Copy(part, file); err != nil {
		return Asset{}, err
	}
	if err := writer.Close(); err != nil {
		return Asset{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/agent/uploads"), &body)
	if err != nil {
		return Asset{}, err
	}
	request.Header.Set("Authorization", "Bearer "+c.key)
	request.Header.Set("User-Agent", "wzstudy-agent/"+c.version)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := c.http.Do(request)
	if err != nil {
		return Asset{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Asset{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &envelope)
		return Asset{}, &Error{Status: response.StatusCode, Code: envelope.Error.Code, Message: envelope.Error.Message}
	}
	var envelope struct {
		Data Asset `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Asset{}, fmt.Errorf("decode asset: %w", err)
	}
	return envelope.Data, nil
}

// retry runs a bounded exponential backoff for transport errors, 429 and 5xx.
// Claim is intentionally single-shot so a network blip cannot silently consume
// a second task.
func (c *Client) retry(ctx context.Context, path string, body any, out any) error {
	delay := time.Second
	for attempt := 0; ; attempt++ {
		_, err := c.do(ctx, http.MethodPost, path, body, out)
		if err == nil {
			return nil
		}
		var apiErr *Error
		retryable := false
		if errors.As(err, &apiErr) {
			retryable = apiErr.Status == http.StatusTooManyRequests || apiErr.Status >= 500
		} else {
			retryable = true
		}
		if !retryable || attempt >= 2 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var out Status
	_, err := c.do(ctx, http.MethodGet, "/agent/status", nil, &out)
	return out, err
}

func (c *Client) Claim(ctx context.Context, capacity, waitSeconds int) (*Task, error) {
	var out struct {
		Task Task `json:"task"`
	}
	status, err := c.do(ctx, http.MethodPost, "/agent/tasks/claim", map[string]any{
		"capacity": capacity, "wait_seconds": waitSeconds,
		"version": c.version, "hostname": hostname(),
	}, &out)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNoContent {
		return nil, nil
	}
	return &out.Task, nil
}

func (c *Client) Heartbeat(ctx context.Context, taskID string) (Heartbeat, error) {
	var out Heartbeat
	err := c.retry(ctx, "/agent/tasks/"+url.PathEscape(taskID)+"/heartbeat", map[string]any{}, &out)
	return out, err
}

func (c *Client) Events(ctx context.Context, taskID string, attempt int, events []EventInput) error {
	return c.retry(ctx, "/agent/tasks/"+url.PathEscape(taskID)+"/events", map[string]any{"attempt": attempt, "events": events}, nil)
}

func (c *Client) PushItems(ctx context.Context, taskID string, attempt int, items []Content) (Receipt, error) {
	var out Receipt
	err := c.retry(ctx, "/agent/tasks/"+url.PathEscape(taskID)+"/items", map[string]any{"attempt": attempt, "items": items}, &out)
	return out, err
}

func (c *Client) Complete(ctx context.Context, taskID string, attempt int, status, summary string, stats Stats, taskErr *TaskError) error {
	return c.retry(ctx, "/agent/tasks/"+url.PathEscape(taskID)+"/complete", map[string]any{
		"attempt": attempt, "status": status, "summary": summary, "stats": stats, "error": taskErr,
	}, nil)
}

func (c *Client) Release(ctx context.Context, taskID string, attempt int, reason string) error {
	return c.retry(ctx, "/agent/tasks/"+url.PathEscape(taskID)+"/release", map[string]any{"attempt": attempt, "reason": reason}, nil)
}

func hostname() string {
	name, err := osHostname()
	if err != nil || len(name) > 200 {
		return ""
	}
	return name
}
