package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

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
