package confluence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	defaultHTTPTimeout     = 60 * time.Second
	defaultDownloadTimeout = 30 * time.Minute
	defaultArchiveTimeout  = 2 * time.Minute
	defaultArchivePollWait = 2 * time.Second
	defaultUserAgent       = "conf/dev"
	maxErrorBodyBytes      = 1 << 20 // 1 MiB
)

const (
	// DefaultArchiveTaskTimeout is the default max wait time for archive long-task completion.
	DefaultArchiveTaskTimeout = defaultArchiveTimeout
	// DefaultArchiveTaskPollInterval is the default archive long-task polling interval.
	DefaultArchiveTaskPollInterval = defaultArchivePollWait
)

// ClientConfig configures the Confluence HTTP client.
type ClientConfig struct {
	BaseURL    string
	Email      string
	APIToken   string //nolint:gosec // Not a hardcoded secret
	HTTPClient *http.Client
	UserAgent  string

	RateLimitRPS     int
	RetryMaxAttempts int
	RetryBaseDelay   time.Duration
	RetryMaxDelay    time.Duration
}

// Client is an HTTP-backed Confluence API client.
type Client struct {
	baseURL        string
	email          string
	apiToken       string
	httpClient     *http.Client
	downloadClient *http.Client
	limiter        *rateLimiter
	retry          retryPolicy
	userAgent      string
}

// NewClient creates a Confluence HTTP client.
func NewClient(cfg ClientConfig) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	email := strings.TrimSpace(cfg.Email)
	token := strings.TrimSpace(cfg.APIToken)

	if baseURL == "" {
		return nil, errors.New("confluence base URL is required")
	}
	if email == "" {
		return nil, errors.New("confluence email is required")
	}
	if token == "" {
		return nil, errors.New("confluence API token is required")
	}
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, fmt.Errorf("invalid confluence base URL: %w", err)
	}

	var transport http.RoundTripper
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		// Clone DefaultTransport so both clients share the same connection pool
		// and TLS settings, but we can tune timeouts independently.
		t := http.DefaultTransport.(*http.Transport).Clone()
		transport = t
		httpClient = &http.Client{
			Timeout:   defaultHTTPTimeout,
			Transport: transport,
		}
	} else {
		transport = httpClient.Transport
	}

	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}

	rateLimitRPS := cfg.RateLimitRPS
	if rateLimitRPS <= 0 {
		rateLimitRPS = DefaultRateLimitRPS
	}

	retryAttempts := cfg.RetryMaxAttempts
	if retryAttempts < 0 {
		retryAttempts = 0
	}
	if cfg.RetryMaxAttempts == 0 {
		retryAttempts = DefaultRetryMaxAttempts
	}
	retry := newRetryPolicy(retryAttempts, cfg.RetryBaseDelay, cfg.RetryMaxDelay)

	downloadClient := &http.Client{
		Timeout:   defaultDownloadTimeout,
		Transport: transport,
	}

	return &Client{
		baseURL:        baseURL,
		email:          email,
		apiToken:       token,
		httpClient:     httpClient,
		downloadClient: downloadClient,
		limiter:        newRateLimiter(rateLimitRPS),
		retry:          retry,
		userAgent:      userAgent,
	}, nil
}

// Close releases background resources used by the client.
func (c *Client) Close() error {
	if c == nil || c.limiter == nil {
		return nil
	}
	c.limiter.stop()
	return nil
}

type userDTO struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

// GetUser retrieves a Confluence user by account ID.
func (c *Client) GetUser(ctx context.Context, accountID string) (User, error) {
	id := strings.TrimSpace(accountID)
	if id == "" {
		return User{}, errors.New("account ID is required")
	}

	req, err := c.newRequest(
		ctx,
		http.MethodGet,
		"/wiki/rest/api/user",
		url.Values{"accountId": []string{id}},
		nil,
	)
	if err != nil {
		return User{}, err
	}

	var payload userDTO
	if err := c.do(req, &payload); err != nil {
		if isHTTPStatus(err, http.StatusNotFound) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}

	return User(payload), nil
}

func (c *Client) newRequest(
	ctx context.Context,
	method string,
	pathSuffix string,
	query url.Values,
	body any,
) (*http.Request, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, pathSuffix)

	if query != nil {
		q := u.Query()
		for key, vals := range query {
			for _, v := range vals {
				q.Add(key, v)
			}
		}
		u.RawQuery = q.Encode()
	}

	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), bodyReader)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.email, c.apiToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) do(req *http.Request, out any) error {
	slog.Debug("http request", "method", req.Method, "url", req.URL.String()) //nolint:gosec // Safe log

	if err := c.limiter.wait(req.Context()); err != nil {
		return err
	}

	for attempt := 0; ; attempt++ {
		resp, err := c.httpClient.Do(req) //nolint:gosec // Target URL comes from API client internals
		if err != nil {
			if c.retry.shouldRetry(req, nil, err, attempt) {
				delay := c.retry.retryDelay(attempt+1, nil)
				slog.Info("http retry", //nolint:gosec // Safe log
					"method", req.Method,
					"url", req.URL.String(),
					"attempt", attempt+1,
					"delay_ms", delay.Milliseconds(),
					"reason", "network_error",
					"error", err,
				)
				if sleepErr := contextSleep(req.Context(), delay); sleepErr != nil {
					return sleepErr
				}
				if req.GetBody != nil {
					newBody, gbErr := req.GetBody()
					if gbErr != nil {
						return fmt.Errorf("reset request body for retry: %w", gbErr)
					}
					req.Body = newBody
				}
				continue
			}
			return err
		}

		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
			_ = resp.Body.Close()

			if c.retry.shouldRetry(req, resp, nil, attempt) {
				delay := c.retry.retryDelay(attempt+1, resp)
				slog.Info("http retry", //nolint:gosec // Safe log
					"method", req.Method,
					"url", req.URL.String(),
					"attempt", attempt+1,
					"delay_ms", delay.Milliseconds(),
					"reason", "status_code",
					"status", resp.StatusCode,
				)
				if sleepErr := contextSleep(req.Context(), delay); sleepErr != nil {
					return sleepErr
				}
				if req.GetBody != nil {
					newBody, gbErr := req.GetBody()
					if gbErr != nil {
						return fmt.Errorf("reset request body for retry: %w", gbErr)
					}
					req.Body = newBody
				}
				continue
			}

			return &APIError{
				StatusCode: resp.StatusCode,
				Method:     req.Method,
				URL:        req.URL.String(),
				Message:    decodeAPIErrorMessage(bodyBytes),
				Body:       string(bodyBytes),
			}
		}

		defer func() {
			_ = resp.Body.Close()
		}()

		if out == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			return nil
		}

		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("decode response JSON: %w", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
}

// Shared list response wrapper used by spaces, pages, and attachments.
type v2ListResponse[T any] struct {
	Results []T    `json:"results"`
	Cursor  string `json:"cursor"`
	Meta    struct {
		Cursor string `json:"cursor"`
	} `json:"meta"`
	Links struct {
		Next string `json:"next"`
	} `json:"_links"`
}

func extractCursor(candidates ...string) string {
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if strings.Contains(candidate, "cursor=") {
			nextURL, err := url.Parse(candidate)
			if err == nil {
				if cursor := nextURL.Query().Get("cursor"); cursor != "" {
					return cursor
				}
			}
		}
		return candidate
	}
	return ""
}

func resolveWebURL(baseURL, webUI string) string {
	if strings.TrimSpace(webUI) == "" {
		return ""
	}
	u, err := url.Parse(webUI)
	if err == nil && u.IsAbs() {
		return webUI
	}
	root, err := url.Parse(baseURL)
	if err != nil {
		return webUI
	}

	contextPath := root.Path
	if contextPath == "" || contextPath == "/" {
		if strings.HasSuffix(root.Host, ".atlassian.net") {
			contextPath = "/wiki"
		}
	}

	if strings.HasPrefix(u.Path, "/") && contextPath != "" && contextPath != "/" {
		if !strings.HasPrefix(u.Path, contextPath) {
			u.Path = path.Join(contextPath, u.Path)
		}
	}

	return root.ResolveReference(u).String()
}

func parseRemoteTime(candidates ...string) time.Time {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		for _, layout := range []string{
			time.RFC3339,
			time.RFC3339Nano,
			"2006-01-02T15:04:05.000Z0700",
			"2006-01-02T15:04:05.000Z07:00",
		} {
			t, err := time.Parse(layout, candidate)
			if err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

func normalizeADFValue(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if strings.TrimSpace(asString) == "" {
			return nil
		}
		return json.RawMessage(asString)
	}
	return raw
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
