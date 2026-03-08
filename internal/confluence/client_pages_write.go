package confluence

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

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

func (c *Client) DeletePage(ctx context.Context, pageID string, opts PageDeleteOptions) error {
	id := strings.TrimSpace(pageID)
	if id == "" {
		return errors.New("page ID is required")
	}

	query := url.Values{}
	if opts.Purge {
		query.Set("purge", "true")
	}
	if opts.Draft {
		query.Set("draft", "true")
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

func (c *Client) DeleteFolder(ctx context.Context, folderID string) error {
	id := strings.TrimSpace(folderID)
	if id == "" {
		return errors.New("folder ID is required")
	}

	req, err := c.newRequest(ctx, http.MethodDelete, "/wiki/api/v2/folders/"+url.PathEscape(id), nil, nil)
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

// ListFolders returns a list of folders.
func (c *Client) ListFolders(ctx context.Context, opts FolderListOptions) (FolderListResult, error) {
	query := url.Values{}
	if opts.SpaceID != "" {
		query.Set("space-id", opts.SpaceID)
	}
	if opts.Title != "" {
		query.Set("title", opts.Title)
	}
	if opts.Limit > 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Cursor != "" {
		query.Set("cursor", opts.Cursor)
	}

	req, err := c.newRequest(ctx, http.MethodGet, "/wiki/api/v2/folders", query, nil)
	if err != nil {
		return FolderListResult{}, err
	}

	var payload v2ListResponse[folderDTO]
	if err := c.do(req, &payload); err != nil {
		return FolderListResult{}, err
	}

	out := FolderListResult{
		Folders:    make([]Folder, 0, len(payload.Results)),
		NextCursor: extractCursor(payload.Cursor, payload.Meta.Cursor, payload.Links.Next),
	}
	for _, item := range payload.Results {
		out.Folders = append(out.Folders, item.toModel())
	}
	return out, nil
}

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
