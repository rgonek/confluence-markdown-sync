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
	if opts.Title != "" {
		query.Set("title", opts.Title)
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

// UpdatePage updates a page.

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

// WaitForArchiveTask polls the Confluence long-task endpoint until completion.

// DeletePage deletes a page.

// CreateFolder creates a Confluence folder under a space or parent folder.

// MovePage moves a page to be a child of the target folder.
// Uses the v1 content move API: PUT /wiki/rest/api/content/{id}/move/append/{targetId}
