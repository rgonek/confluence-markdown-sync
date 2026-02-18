package confluence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPTimeout = 30 * time.Second
	defaultUserAgent   = "cms/dev"
	maxErrorBodyBytes  = 1 << 20 // 1 MiB
)

var (
	// ErrNotFound indicates the requested resource does not exist.
	ErrNotFound = errors.New("confluence resource not found")
)

// ClientConfig configures the Confluence HTTP client.
type ClientConfig struct {
	BaseURL    string
	Email      string
	APIToken   string
	HTTPClient *http.Client
	UserAgent  string
}

// Client is an HTTP-backed Confluence API client.
type Client struct {
	baseURL    string
	email      string
	apiToken   string
	httpClient *http.Client
	userAgent  string
}

// APIError is returned for non-2xx responses.
type APIError struct {
	StatusCode int
	Method     string
	URL        string
	Message    string
	Body       string
}

func (e *APIError) Error() string {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = strings.TrimSpace(e.Body)
	}
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	if msg == "" {
		msg = "request failed"
	}
	return fmt.Sprintf("%s %s: status %d: %s", e.Method, e.URL, e.StatusCode, msg)
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

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}

	return &Client{
		baseURL:    baseURL,
		email:      email,
		apiToken:   token,
		httpClient: httpClient,
		userAgent:  userAgent,
	}, nil
}

// ListSpaces returns a list of spaces.
func (c *Client) ListSpaces(ctx context.Context, opts SpaceListOptions) (SpaceListResult, error) {
	query := url.Values{}
	if len(opts.Keys) > 0 {
		query.Set("keys", strings.Join(opts.Keys, ","))
	}
	if opts.Limit > 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Cursor != "" {
		query.Set("cursor", opts.Cursor)
	}

	req, err := c.newRequest(ctx, http.MethodGet, "/wiki/api/v2/spaces", query, nil)
	if err != nil {
		return SpaceListResult{}, err
	}

	var payload v2ListResponse[spaceDTO]
	if err := c.do(req, &payload); err != nil {
		return SpaceListResult{}, err
	}

	out := SpaceListResult{
		Spaces:     make([]Space, 0, len(payload.Results)),
		NextCursor: extractCursor(payload.Cursor, payload.Meta.Cursor, payload.Links.Next),
	}
	for _, item := range payload.Results {
		out.Spaces = append(out.Spaces, item.toModel())
	}
	return out, nil
}

// GetSpace finds a space by key.
func (c *Client) GetSpace(ctx context.Context, spaceKey string) (Space, error) {
	key := strings.TrimSpace(spaceKey)
	if key == "" {
		return Space{}, errors.New("space key is required")
	}

	result, err := c.ListSpaces(ctx, SpaceListOptions{
		Keys:  []string{key},
		Limit: 1,
	})
	if err != nil {
		return Space{}, err
	}
	for _, item := range result.Spaces {
		if strings.EqualFold(item.Key, key) {
			return item, nil
		}
	}
	return Space{}, ErrNotFound
}

// ListPages returns a list of pages.
func (c *Client) ListPages(ctx context.Context, opts PageListOptions) (PageListResult, error) {
	query := url.Values{}
	if opts.SpaceID != "" {
		query.Set("space-id", opts.SpaceID)
	}
	if opts.SpaceKey != "" {
		query.Set("space-key", opts.SpaceKey)
	}
	if opts.Status != "" {
		query.Set("status", opts.Status)
	}
	if opts.Limit > 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Cursor != "" {
		query.Set("cursor", opts.Cursor)
	}

	req, err := c.newRequest(ctx, http.MethodGet, "/wiki/api/v2/pages", query, nil)
	if err != nil {
		return PageListResult{}, err
	}

	var payload v2ListResponse[pageDTO]
	if err := c.do(req, &payload); err != nil {
		return PageListResult{}, err
	}

	out := PageListResult{
		Pages:      make([]Page, 0, len(payload.Results)),
		NextCursor: extractCursor(payload.Cursor, payload.Meta.Cursor, payload.Links.Next),
	}
	for _, item := range payload.Results {
		out.Pages = append(out.Pages, item.toModel(c.baseURL))
	}
	return out, nil
}

// GetPage fetches a single page by ID.
func (c *Client) GetPage(ctx context.Context, pageID string) (Page, error) {
	id := strings.TrimSpace(pageID)
	if id == "" {
		return Page{}, errors.New("page ID is required")
	}

	req, err := c.newRequest(
		ctx,
		http.MethodGet,
		"/wiki/api/v2/pages/"+url.PathEscape(id),
		url.Values{"body-format": []string{"atlas_doc_format"}},
		nil,
	)
	if err != nil {
		return Page{}, err
	}

	var payload pageDTO
	if err := c.do(req, &payload); err != nil {
		if isHTTPStatus(err, http.StatusNotFound) {
			return Page{}, ErrNotFound
		}
		return Page{}, err
	}
	return payload.toModel(c.baseURL), nil
}

// CreatePage creates a page.
func (c *Client) CreatePage(ctx context.Context, input PageUpsertInput) (Page, error) {
	if strings.TrimSpace(input.SpaceID) == "" {
		return Page{}, errors.New("space ID is required")
	}
	if strings.TrimSpace(input.Title) == "" {
		return Page{}, errors.New("page title is required")
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/wiki/api/v2/pages", nil, pageWritePayload(input))
	if err != nil {
		return Page{}, err
	}

	var payload pageDTO
	if err := c.do(req, &payload); err != nil {
		return Page{}, err
	}
	return payload.toModel(c.baseURL), nil
}

// UpdatePage updates a page.
func (c *Client) UpdatePage(ctx context.Context, pageID string, input PageUpsertInput) (Page, error) {
	id := strings.TrimSpace(pageID)
	if id == "" {
		return Page{}, errors.New("page ID is required")
	}
	if strings.TrimSpace(input.Title) == "" {
		return Page{}, errors.New("page title is required")
	}

	req, err := c.newRequest(
		ctx,
		http.MethodPut,
		"/wiki/api/v2/pages/"+url.PathEscape(id),
		nil,
		pageWritePayload(input),
	)
	if err != nil {
		return Page{}, err
	}

	var payload pageDTO
	if err := c.do(req, &payload); err != nil {
		if isHTTPStatus(err, http.StatusNotFound) {
			return Page{}, ErrNotFound
		}
		return Page{}, err
	}
	return payload.toModel(c.baseURL), nil
}

// ListChanges lists changed pages for a space.
func (c *Client) ListChanges(ctx context.Context, opts ChangeListOptions) (ChangeListResult, error) {
	spaceKey := strings.TrimSpace(opts.SpaceKey)
	if spaceKey == "" {
		return ChangeListResult{}, errors.New("space key is required")
	}

	query := url.Values{}
	query.Set("cql", buildChangeCQL(spaceKey, opts.Since))
	if opts.Limit > 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Start > 0 {
		query.Set("start", strconv.Itoa(opts.Start))
	}

	req, err := c.newRequest(ctx, http.MethodGet, "/wiki/rest/api/content/search", query, nil)
	if err != nil {
		return ChangeListResult{}, err
	}

	var payload changeSearchResponse
	if err := c.do(req, &payload); err != nil {
		return ChangeListResult{}, err
	}

	out := ChangeListResult{
		Changes: make([]Change, 0, len(payload.Results)),
		HasMore: payload.Size == payload.Limit && payload.Size > 0,
	}
	out.NextStart = extractNextStart(payload.Start, payload.Links.Next)
	if out.NextStart > payload.Start {
		out.HasMore = true
	}
	for _, item := range payload.Results {
		out.Changes = append(out.Changes, item.toModel())
	}
	return out, nil
}

// ArchivePages archives pages in bulk and returns the archive task ID.
func (c *Client) ArchivePages(ctx context.Context, pageIDs []string) (ArchiveResult, error) {
	if len(pageIDs) == 0 {
		return ArchiveResult{}, errors.New("at least one page ID is required")
	}
	pages := make([]archivePageInput, 0, len(pageIDs))
	for _, id := range pageIDs {
		clean := strings.TrimSpace(id)
		if clean == "" {
			return ArchiveResult{}, errors.New("page IDs must be non-empty")
		}
		pages = append(pages, archivePageInput{ID: clean})
	}

	req, err := c.newRequest(
		ctx,
		http.MethodPost,
		"/wiki/rest/api/content/archive",
		nil,
		archiveRequest{Pages: pages},
	)
	if err != nil {
		return ArchiveResult{}, err
	}

	var payload archiveResponse
	if err := c.do(req, &payload); err != nil {
		return ArchiveResult{}, err
	}
	return ArchiveResult{TaskID: payload.ID}, nil
}

// DeletePage deletes a page.
func (c *Client) DeletePage(ctx context.Context, pageID string, hardDelete bool) error {
	id := strings.TrimSpace(pageID)
	if id == "" {
		return errors.New("page ID is required")
	}

	query := url.Values{}
	if hardDelete {
		query.Set("purge", "true")
	}

	req, err := c.newRequest(
		ctx,
		http.MethodDelete,
		"/wiki/api/v2/pages/"+url.PathEscape(id),
		query,
		nil,
	)
	if err != nil {
		return err
	}
	if err := c.do(req, nil); err != nil {
		if isHTTPStatus(err, http.StatusNotFound) {
			return ErrNotFound
		}
		return err
	}
	return nil
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
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &APIError{
			StatusCode: resp.StatusCode,
			Method:     req.Method,
			URL:        req.URL.String(),
			Message:    decodeAPIErrorMessage(bodyBytes),
			Body:       string(bodyBytes),
		}
	}

	if out == nil || len(bodyBytes) == 0 {
		return nil
	}
	if err := json.Unmarshal(bodyBytes, out); err != nil {
		return fmt.Errorf("decode response JSON: %w", err)
	}
	return nil
}

func isHTTPStatus(err error, status int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == status
}

func decodeAPIErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}

	for _, key := range []string{"message", "error", "reason"} {
		if v, ok := payload[key].(string); ok {
			return v
		}
	}
	if v, ok := payload["errors"].([]any); ok && len(v) > 0 {
		if first, ok := v[0].(string); ok {
			return first
		}
	}
	return ""
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

func extractNextStart(current int, nextLink string) int {
	if strings.TrimSpace(nextLink) == "" {
		return current
	}
	nextURL, err := url.Parse(nextLink)
	if err != nil {
		return current
	}
	start := nextURL.Query().Get("start")
	if start == "" {
		return current
	}
	n, err := strconv.Atoi(start)
	if err != nil {
		return current
	}
	return n
}

func buildChangeCQL(spaceKey string, since time.Time) string {
	parts := []string{
		"type=page",
		fmt.Sprintf(`space="%s"`, strings.ReplaceAll(spaceKey, `"`, `\"`)),
	}
	if !since.IsZero() {
		parts = append(parts, fmt.Sprintf(`lastmodified >= "%s"`, since.UTC().Format("2006-01-02 15:04")))
	}
	return strings.Join(parts, " AND ")
}

func pageWritePayload(input PageUpsertInput) map[string]any {
	payload := map[string]any{
		"spaceId": strings.TrimSpace(input.SpaceID),
		"title":   strings.TrimSpace(input.Title),
		"status":  defaultPageStatus(input.Status),
	}
	if input.ParentPageID != "" {
		payload["parentId"] = strings.TrimSpace(input.ParentPageID)
	}
	if input.Version > 0 {
		payload["version"] = map[string]any{
			"number": input.Version,
		}
	}
	if len(input.BodyADF) > 0 {
		payload["body"] = map[string]any{
			"representation": "atlas_doc_format",
			"value":          string(input.BodyADF),
		}
	}
	return payload
}

func defaultPageStatus(v string) string {
	status := strings.TrimSpace(v)
	if status == "" {
		return "current"
	}
	return status
}

type v2ListResponse[T any] struct {
	Results []T `json:"results"`
	Cursor  string `json:"cursor"`
	Meta    struct {
		Cursor string `json:"cursor"`
	} `json:"meta"`
	Links struct {
		Next string `json:"next"`
	} `json:"_links"`
}

type spaceDTO struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
	Type string `json:"type"`
}

func (s spaceDTO) toModel() Space {
	return Space{
		ID:   s.ID,
		Key:  s.Key,
		Name: s.Name,
		Type: s.Type,
	}
}

type pageDTO struct {
	ID       string `json:"id"`
	SpaceID  string `json:"spaceId"`
	Status   string `json:"status"`
	Title    string `json:"title"`
	ParentID string `json:"parentId"`
	Version  struct {
		Number    int    `json:"number"`
		CreatedAt string `json:"createdAt"`
		When      string `json:"when"`
	} `json:"version"`
	History struct {
		LastUpdated struct {
			When string `json:"when"`
		} `json:"lastUpdated"`
	} `json:"history"`
	Body struct {
		AtlasDocFormat struct {
			Value json.RawMessage `json:"value"`
		} `json:"atlas_doc_format"`
	} `json:"body"`
	Links struct {
		WebUI string `json:"webui"`
	} `json:"_links"`
}

func (p pageDTO) toModel(baseURL string) Page {
	return Page{
		ID:           p.ID,
		SpaceID:      p.SpaceID,
		Title:        p.Title,
		Status:       p.Status,
		ParentPageID: p.ParentID,
		Version:      p.Version.Number,
		LastModified: parseRemoteTime(p.Version.CreatedAt, p.Version.When, p.History.LastUpdated.When),
		WebURL:       resolveWebURL(baseURL, p.Links.WebUI),
		BodyADF:      normalizeADFValue(p.Body.AtlasDocFormat.Value),
	}
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
	ref, err := url.Parse(webUI)
	if err != nil {
		return webUI
	}
	return root.ResolveReference(ref).String()
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

type changeSearchResponse struct {
	Results []changeResultDTO `json:"results"`
	Start   int `json:"start"`
	Limit   int `json:"limit"`
	Size    int `json:"size"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}

type changeResultDTO struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Space   struct {
		Key string `json:"key"`
	} `json:"space"`
	Version struct {
		Number int    `json:"number"`
		When   string `json:"when"`
	} `json:"version"`
	History struct {
		LastUpdated struct {
			When string `json:"when"`
		} `json:"lastUpdated"`
	} `json:"history"`
}

func (c changeResultDTO) toModel() Change {
	return Change{
		PageID:       c.ID,
		SpaceKey:     c.Space.Key,
		Title:        c.Title,
		Version:      c.Version.Number,
		LastModified: parseRemoteTime(c.Version.When, c.History.LastUpdated.When),
	}
}

type archiveRequest struct {
	Pages []archivePageInput `json:"pages"`
}

type archivePageInput struct {
	ID string `json:"id"`
}

type archiveResponse struct {
	ID string `json:"id"`
}

