package confluence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
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

var (
	// ErrNotFound indicates the requested resource does not exist.
	ErrNotFound = errors.New("confluence resource not found")
	// ErrArchived indicates the requested page is already archived.
	ErrArchived = errors.New("confluence page archived")
	// ErrArchiveTaskFailed indicates Confluence long-task failure.
	ErrArchiveTaskFailed = errors.New("confluence archive task failed")
	// ErrArchiveTaskTimeout indicates archive long-task polling timed out.
	ErrArchiveTaskTimeout = errors.New("confluence archive task timeout")
)

// ClientConfig configures the Confluence HTTP client.
type ClientConfig struct {
	BaseURL    string
	Email      string
	APIToken   string
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
	status := opts.Status
	if status == "" {
		status = "current"
	}
	query.Set("status", status)

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

// GetFolder fetches a single folder by ID.
func (c *Client) GetFolder(ctx context.Context, folderID string) (Folder, error) {
	id := strings.TrimSpace(folderID)
	if id == "" {
		return Folder{}, errors.New("folder ID is required")
	}

	req, err := c.newRequest(
		ctx,
		http.MethodGet,
		"/wiki/api/v2/folders/"+url.PathEscape(id),
		nil,
		nil,
	)
	if err != nil {
		return Folder{}, err
	}

	var payload folderDTO
	if err := c.do(req, &payload); err != nil {
		if isHTTPStatus(err, http.StatusNotFound) {
			return Folder{}, ErrNotFound
		}
		return Folder{}, err
	}

	return payload.toModel(), nil
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
		if isArchivedAPIError(err) {
			return Page{}, ErrArchived
		}
		return Page{}, err
	}
	return payload.toModel(c.baseURL), nil
}

// ListAttachments fetches attachments for a page.
func (c *Client) ListAttachments(ctx context.Context, pageID string) ([]Attachment, error) {
	pageID = strings.TrimSpace(pageID)
	if pageID == "" {
		return nil, errors.New("page ID is required")
	}

	query := url.Values{}
	query.Set("limit", "100")

	req, err := c.newRequest(ctx, http.MethodGet, "/wiki/api/v2/pages/"+url.PathEscape(pageID)+"/attachments", query, nil)
	if err != nil {
		return nil, err
	}

	attachments := []Attachment{}
	var payload v2ListResponse[attachmentDTO]
	for {
		if err := c.do(req, &payload); err != nil {
			if isHTTPStatus(err, http.StatusNotFound) {
				return nil, ErrNotFound
			}
			return nil, err
		}

		for _, item := range payload.Results {
			attachmentID := strings.TrimSpace(item.ID)
			if attachmentID == "" {
				continue
			}

			attachments = append(attachments, Attachment{
				ID:        attachmentID,
				PageID:    pageID,
				Filename:  firstNonEmpty(item.Title, item.Filename),
				MediaType: item.MediaType,
			})
		}

		nextURLStr := strings.TrimSpace(payload.Links.Next)
		if nextURLStr == "" {
			break
		}

		if !strings.HasPrefix(nextURLStr, "http") {
			nextURLStr = resolveWebURL(c.baseURL, nextURLStr)
		}

		req, err = http.NewRequestWithContext(ctx, http.MethodGet, nextURLStr, nil)
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth(c.email, c.apiToken)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)

		payload = v2ListResponse[attachmentDTO]{}
	}

	return attachments, nil
}

// DownloadAttachment downloads attachment bytes by attachment ID.
func (c *Client) DownloadAttachment(ctx context.Context, attachmentID string, pageID string, out io.Writer) error {
	id := strings.TrimSpace(attachmentID)
	if id == "" {
		return errors.New("attachment ID is required")
	}

	if isUUID(id) {
		if resolvedID, err := c.resolveAttachmentIDByFileID(ctx, id, pageID); err == nil {
			id = resolvedID
		}
	}

	req, err := c.newRequest(
		ctx,
		http.MethodGet,
		"/wiki/api/v2/attachments/"+url.PathEscape(id),
		nil,
		nil,
	)
	if err != nil {
		return err
	}

	var payload attachmentDTO
	if err := c.do(req, &payload); err != nil {
		if isHTTPStatus(err, http.StatusNotFound) {
			return ErrNotFound
		}
		return err
	}

	downloadURL := strings.TrimSpace(payload.DownloadLink)
	if downloadURL == "" {
		downloadURL = strings.TrimSpace(payload.Links.Download)
	}
	if downloadURL == "" {
		downloadURL = "/wiki/api/v2/attachments/" + url.PathEscape(id) + "/download"
	}

	resolvedDownloadURL := resolveWebURL(c.baseURL, downloadURL)
	if strings.TrimSpace(resolvedDownloadURL) == "" {
		return fmt.Errorf("attachment %s download URL is empty", id)
	}

	downloadReq, err := http.NewRequestWithContext(ctx, http.MethodGet, resolvedDownloadURL, nil)
	if err != nil {
		return err
	}

	// Only send Basic Auth if the download URL is on the same host as our base URL.
	// Many Confluence attachments redirect to external media services (like S3)
	// which will reject the request if an unexpected Authorization header is present.
	if u, err := url.Parse(resolvedDownloadURL); err == nil {
		if baseU, err := url.Parse(c.baseURL); err == nil {
			if u.Host == baseU.Host {
				downloadReq.SetBasicAuth(c.email, c.apiToken)
			}
		}
	}

	downloadReq.Header.Set("Accept", "*/*")
	downloadReq.Header.Set("User-Agent", c.userAgent)

	slog.Debug("http request", "method", downloadReq.Method, "url", downloadReq.URL.String())

	resp, err := c.downloadClient.Do(downloadReq)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		if resp.StatusCode == http.StatusNotFound {
			return ErrNotFound
		}
		return &APIError{
			StatusCode: resp.StatusCode,
			Method:     downloadReq.Method,
			URL:        downloadReq.URL.String(),
			Message:    decodeAPIErrorMessage(bodyBytes),
			Body:       string(bodyBytes),
		}
	}

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("write attachment response: %w", err)
	}

	return nil
}

// UploadAttachment uploads an attachment to a page.
func (c *Client) UploadAttachment(ctx context.Context, input AttachmentUploadInput) (Attachment, error) {
	pageID := strings.TrimSpace(input.PageID)
	if pageID == "" {
		return Attachment{}, errors.New("page ID is required")
	}
	filename := strings.TrimSpace(input.Filename)
	if filename == "" {
		return Attachment{}, errors.New("filename is required")
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	filePart, err := writer.CreateFormFile("file", filepath.Base(filename))
	if err != nil {
		return Attachment{}, fmt.Errorf("create multipart file part: %w", err)
	}
	if _, err := filePart.Write(input.Data); err != nil {
		return Attachment{}, fmt.Errorf("write multipart payload: %w", err)
	}
	if err := writer.Close(); err != nil {
		return Attachment{}, fmt.Errorf("close multipart payload: %w", err)
	}

	u, err := url.Parse(c.baseURL)
	if err != nil {
		return Attachment{}, err
	}
	u.Path = path.Join(u.Path, "/wiki/rest/api/content", url.PathEscape(pageID), "child", "attachment")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), body)
	if err != nil {
		return Attachment{}, err
	}
	req.SetBasicAuth(c.email, c.apiToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("X-Atlassian-Token", "no-check")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	var payload attachmentUploadResponse
	if err := c.do(req, &payload); err != nil {
		if isHTTPStatus(err, http.StatusNotFound) {
			return Attachment{}, ErrNotFound
		}
		return Attachment{}, err
	}
	if len(payload.Results) == 0 {
		return Attachment{}, errors.New("upload attachment response missing results")
	}

	item := payload.Results[0]
	if strings.TrimSpace(item.ID) == "" {
		return Attachment{}, errors.New("upload attachment response missing id")
	}

	resolvedWebURL := resolveWebURL(c.baseURL, item.Links.WebUI)
	if strings.TrimSpace(resolvedWebURL) == "" {
		resolvedWebURL = resolveWebURL(c.baseURL, item.Links.Download)
	}

	return Attachment{
		ID:        item.ID,
		PageID:    pageID,
		Filename:  firstNonEmpty(item.Title, item.Filename, filepath.Base(filename)),
		MediaType: item.MediaType,
		WebURL:    resolvedWebURL,
	}, nil
}

// DeleteAttachment deletes a Confluence attachment.
func (c *Client) DeleteAttachment(ctx context.Context, attachmentID string, pageID string) error {
	id := strings.TrimSpace(attachmentID)
	if id == "" {
		return errors.New("attachment ID is required")
	}

	if isUUID(id) && pageID != "" {
		if resolvedID, err := c.resolveAttachmentIDByFileID(ctx, id, pageID); err == nil {
			id = resolvedID
		}
	}

	req, err := c.newRequest(
		ctx,
		http.MethodDelete,
		"/wiki/api/v2/attachments/"+url.PathEscape(id),
		nil,
		nil,
	)
	if err != nil {
		return err
	}
	if err := c.do(req, nil); err != nil {
		if isHTTPStatus(err, http.StatusNotFound) {
			return ErrNotFound
		}
		if isInvalidAttachmentIdentifierError(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// CreatePage creates a page.
func (c *Client) CreatePage(ctx context.Context, input PageUpsertInput) (Page, error) {
	if strings.TrimSpace(input.SpaceID) == "" {
		return Page{}, errors.New("space ID is required")
	}
	if strings.TrimSpace(input.Title) == "" {
		return Page{}, errors.New("page title is required")
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/wiki/api/v2/pages", nil, pageWritePayload("", input))
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
		pageWritePayload(id, input),
	)
	if err != nil {
		return Page{}, err
	}

	var payload pageDTO
	if err := c.do(req, &payload); err != nil {
		if isHTTPStatus(err, http.StatusNotFound) {
			return Page{}, ErrNotFound
		}
		if isArchivedAPIError(err) {
			return Page{}, ErrArchived
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
		if isArchivedAPIError(err) {
			return ArchiveResult{}, ErrArchived
		}
		return ArchiveResult{}, err
	}
	return ArchiveResult{TaskID: payload.ID}, nil
}

// WaitForArchiveTask polls the Confluence long-task endpoint until completion.
func (c *Client) WaitForArchiveTask(ctx context.Context, taskID string, opts ArchiveTaskWaitOptions) (ArchiveTaskStatus, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ArchiveTaskStatus{}, errors.New("archive task ID is required")
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultArchiveTaskTimeout
	}
	pollInterval := opts.PollInterval
	if pollInterval <= 0 {
		pollInterval = DefaultArchiveTaskPollInterval
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	last := ArchiveTaskStatus{TaskID: taskID, State: ArchiveTaskStateInProgress}
	for {
		status, err := c.getArchiveTaskStatus(waitCtx, taskID)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return last, fmt.Errorf("%w: task %s exceeded %s", ErrArchiveTaskTimeout, taskID, timeout)
			}
			if errors.Is(err, context.Canceled) {
				return last, err
			}
			return last, fmt.Errorf("poll archive task %s: %w", taskID, err)
		}
		last = status

		switch status.State {
		case ArchiveTaskStateSucceeded:
			return status, nil
		case ArchiveTaskStateFailed:
			message := strings.TrimSpace(status.Message)
			if message == "" {
				message = strings.TrimSpace(status.RawStatus)
			}
			if message == "" {
				message = "task reported failure"
			}
			return status, fmt.Errorf("%w: task %s: %s", ErrArchiveTaskFailed, taskID, message)
		}

		if pollInterval <= 0 {
			pollInterval = DefaultArchiveTaskPollInterval
		}

		if err := contextSleep(waitCtx, pollInterval); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return last, fmt.Errorf("%w: task %s exceeded %s", ErrArchiveTaskTimeout, taskID, timeout)
			}
			return last, err
		}
	}
}

func (c *Client) getArchiveTaskStatus(ctx context.Context, taskID string) (ArchiveTaskStatus, error) {
	req, err := c.newRequest(
		ctx,
		http.MethodGet,
		"/wiki/rest/api/longtask/"+url.PathEscape(taskID),
		nil,
		nil,
	)
	if err != nil {
		return ArchiveTaskStatus{}, err
	}

	var payload longTaskResponse
	if err := c.do(req, &payload); err != nil {
		if isHTTPStatus(err, http.StatusNotFound) {
			return ArchiveTaskStatus{}, ErrNotFound
		}
		return ArchiveTaskStatus{}, err
	}

	status := payload.toArchiveTaskStatus(taskID)
	if status.TaskID == "" {
		status.TaskID = taskID
	}
	return status, nil
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

// CreateFolder creates a Confluence folder under a space or parent folder.
func (c *Client) CreateFolder(ctx context.Context, input FolderCreateInput) (Folder, error) {
	if strings.TrimSpace(input.SpaceID) == "" {
		return Folder{}, errors.New("space ID is required")
	}
	if strings.TrimSpace(input.Title) == "" {
		return Folder{}, errors.New("folder title is required")
	}

	parentType := input.ParentType
	if parentType == "" {
		if input.ParentID != "" {
			parentType = "folder"
		} else {
			parentType = "space"
		}
	}

	body := map[string]any{
		"spaceId":    strings.TrimSpace(input.SpaceID),
		"title":      strings.TrimSpace(input.Title),
		"parentType": parentType,
	}
	if input.ParentID != "" {
		body["parentId"] = strings.TrimSpace(input.ParentID)
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/wiki/api/v2/folders", nil, body)
	if err != nil {
		return Folder{}, err
	}

	var payload folderDTO
	if err := c.do(req, &payload); err != nil {
		return Folder{}, err
	}
	return payload.toModel(), nil
}

// MovePage moves a page to be a child of the target folder.
// Uses the v1 content move API: PUT /wiki/rest/api/content/{id}/move/append/{targetId}
func (c *Client) MovePage(ctx context.Context, pageID string, targetID string) error {
	id := strings.TrimSpace(pageID)
	if id == "" {
		return errors.New("page ID is required")
	}
	target := strings.TrimSpace(targetID)
	if target == "" {
		return errors.New("target ID is required")
	}

	req, err := c.newRequest(
		ctx,
		http.MethodPut,
		"/wiki/rest/api/content/"+url.PathEscape(id)+"/move/append/"+url.PathEscape(target),
		nil,
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
	slog.Debug("http request", "method", req.Method, "url", req.URL.String())

	if err := c.limiter.wait(req.Context()); err != nil {
		return err
	}

	for attempt := 0; ; attempt++ {
		resp, err := c.httpClient.Do(req)
		if err != nil {
			if c.retry.shouldRetry(req, nil, err, attempt) {
				delay := c.retry.retryDelay(attempt+1, nil)
				slog.Info("http retry",
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
				slog.Info("http retry",
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

func isHTTPStatus(err error, status int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == status
}

func isInvalidAttachmentIdentifierError(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return false
	}
	body := strings.ToLower(strings.TrimSpace(apiErr.Body))
	message := strings.ToLower(strings.TrimSpace(apiErr.Message))
	combined := message + " " + body
	return strings.Contains(combined, "invalid_request_parameter") &&
		(strings.Contains(combined, "expected type is contentid") || strings.Contains(combined, "for 'id'"))
}

func isArchivedAPIError(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}

	switch apiErr.StatusCode {
	case http.StatusBadRequest, http.StatusConflict, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity:
		// continue
	default:
		return false
	}

	combined := strings.ToLower(strings.TrimSpace(apiErr.Message + " " + apiErr.Body))
	if combined == "" {
		return false
	}

	if strings.Contains(combined, "already archived") {
		return true
	}
	if strings.Contains(combined, "is archived") {
		return true
	}
	if strings.Contains(combined, "archived content") {
		return true
	}
	if strings.Contains(combined, "status=archived") || strings.Contains(combined, "status: archived") {
		return true
	}
	if strings.Contains(combined, "cannot update archived") {
		return true
	}

	return false
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

func pageWritePayload(id string, input PageUpsertInput) map[string]any {
	payload := map[string]any{
		"spaceId": strings.TrimSpace(input.SpaceID),
		"title":   strings.TrimSpace(input.Title),
		"status":  defaultPageStatus(input.Status),
	}
	if id != "" {
		payload["id"] = strings.TrimSpace(id)
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
	Results []T    `json:"results"`
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
	return Space(s)
}

type folderDTO struct {
	ID         string `json:"id"`
	SpaceID    string `json:"spaceId"`
	Title      string `json:"title"`
	ParentID   string `json:"parentId"`
	ParentType string `json:"parentType"`
}

func (f folderDTO) toModel() Folder {
	return Folder(f)
}

type pageDTO struct {
	ID         string `json:"id"`
	SpaceID    string `json:"spaceId"`
	Status     string `json:"status"`
	Title      string `json:"title"`
	ParentID   string `json:"parentId"`
	ParentType string `json:"parentType"`
	AuthorID   string `json:"authorId"`
	CreatedAt  string `json:"createdAt"`
	Version    struct {
		Number    int    `json:"number"`
		AuthorID  string `json:"authorId"`
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
		ID:                   p.ID,
		SpaceID:              p.SpaceID,
		Title:                p.Title,
		Status:               p.Status,
		ParentPageID:         p.ParentID,
		ParentType:           p.ParentType,
		Version:              p.Version.Number,
		AuthorID:             p.AuthorID,
		CreatedAt:            parseRemoteTime(p.CreatedAt),
		LastModifiedAuthorID: p.Version.AuthorID,
		LastModified:         parseRemoteTime(p.Version.CreatedAt, p.Version.When, p.History.LastUpdated.When),
		WebURL:               resolveWebURL(baseURL, p.Links.WebUI),
		BodyADF:              normalizeADFValue(p.Body.AtlasDocFormat.Value),
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

type changeSearchResponse struct {
	Results []changeResultDTO `json:"results"`
	Start   int               `json:"start"`
	Limit   int               `json:"limit"`
	Size    int               `json:"size"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}

type changeResultDTO struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Space struct {
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

type longTaskResponse struct {
	ID                 string               `json:"id"`
	Status             string               `json:"status"`
	PercentageComplete int                  `json:"percentageComplete"`
	Finished           *bool                `json:"finished"`
	Successful         *bool                `json:"successful"`
	Messages           []longTaskMessageDTO `json:"messages"`
	ErrorMessage       string               `json:"errorMessage"`
}

type longTaskMessageDTO struct {
	Translation string `json:"translation"`
	Message     string `json:"message"`
	Title       string `json:"title"`
}

func (l longTaskResponse) toArchiveTaskStatus(defaultTaskID string) ArchiveTaskStatus {
	taskID := strings.TrimSpace(l.ID)
	if taskID == "" {
		taskID = strings.TrimSpace(defaultTaskID)
	}

	rawStatus := strings.TrimSpace(l.Status)
	normalizedStatus := strings.ToLower(rawStatus)

	finished := false
	if l.Finished != nil {
		finished = *l.Finished
	}
	successfulKnown := false
	successful := false
	if l.Successful != nil {
		successfulKnown = true
		successful = *l.Successful
	}

	if statusIndicatesTerminal(normalizedStatus) {
		finished = true
	}
	if !successfulKnown && statusIndicatesSuccess(normalizedStatus) {
		successfulKnown = true
		successful = true
	}

	state := ArchiveTaskStateInProgress
	if finished {
		if successfulKnown {
			if successful {
				state = ArchiveTaskStateSucceeded
			} else {
				state = ArchiveTaskStateFailed
			}
		} else if statusIndicatesFailure(normalizedStatus) {
			state = ArchiveTaskStateFailed
		} else {
			state = ArchiveTaskStateSucceeded
		}
	} else if statusIndicatesFailure(normalizedStatus) {
		state = ArchiveTaskStateFailed
	}

	message := strings.TrimSpace(l.ErrorMessage)
	if message == "" {
		for _, candidate := range l.Messages {
			message = firstNonEmpty(candidate.Message, candidate.Translation, candidate.Title)
			if message != "" {
				break
			}
		}
	}

	return ArchiveTaskStatus{
		TaskID:      taskID,
		State:       state,
		RawStatus:   rawStatus,
		Message:     message,
		PercentDone: l.PercentageComplete,
	}
}

func statusIndicatesSuccess(status string) bool {
	if status == "" {
		return false
	}
	for _, token := range []string{"success", "succeeded", "complete", "completed", "done"} {
		if strings.Contains(status, token) {
			return true
		}
	}
	return false
}

func statusIndicatesFailure(status string) bool {
	if status == "" {
		return false
	}
	for _, token := range []string{"fail", "failed", "error", "cancelled", "canceled", "aborted"} {
		if strings.Contains(status, token) {
			return true
		}
	}
	return false
}

func statusIndicatesTerminal(status string) bool {
	return statusIndicatesSuccess(status) || statusIndicatesFailure(status)
}

type attachmentDTO struct {
	ID           string `json:"id"`
	FileID       string `json:"fileId"`
	Title        string `json:"title"`
	Filename     string `json:"filename"`
	MediaType    string `json:"mediaType"`
	DownloadLink string `json:"downloadLink"`
	Links        struct {
		Download string `json:"download"`
	} `json:"_links"`
}

type attachmentUploadResponse struct {
	Results []attachmentUploadResultDTO `json:"results"`
}

type attachmentUploadResultDTO struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Filename  string `json:"filename"`
	MediaType string `json:"mediaType"`
	Links     struct {
		WebUI    string `json:"webui"`
		Download string `json:"download"`
	} `json:"_links"`
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

func (c *Client) resolveAttachmentIDByFileID(ctx context.Context, fileID string, pageID string) (string, error) {
	if pageID == "" {
		return "", errors.New("page ID is required to resolve file ID")
	}

	query := url.Values{}
	query.Set("limit", "100")

	req, err := c.newRequest(ctx, http.MethodGet, "/wiki/api/v2/pages/"+url.PathEscape(pageID)+"/attachments", query, nil)
	if err != nil {
		return "", err
	}

	var payload v2ListResponse[attachmentDTO]
	for {
		if err := c.do(req, &payload); err != nil {
			return "", err
		}

		for _, att := range payload.Results {
			if att.FileID == fileID {
				return att.ID, nil
			}
		}

		nextURLStr := payload.Links.Next
		if nextURLStr == "" {
			break
		}

		// Ensure nextURL is a full URL or relative to base
		if !strings.HasPrefix(nextURLStr, "http") {
			nextURLStr = resolveWebURL(c.baseURL, nextURLStr)
		}

		req, err = http.NewRequestWithContext(ctx, http.MethodGet, nextURLStr, nil)
		if err != nil {
			return "", err
		}
		req.SetBasicAuth(c.email, c.apiToken)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)

		payload = v2ListResponse[attachmentDTO]{}
	}

	return "", ErrNotFound
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
				return false
			}
		}
	}
	return true
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
