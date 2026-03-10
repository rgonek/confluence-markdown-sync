package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var contentStateColorPattern = regexp.MustCompile(`^[0-9A-Fa-f]{6}$`)

type contentStateDTO struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

func (d contentStateDTO) toModel() ContentState {
	return ContentState{
		ID:    d.ID,
		Name:  strings.TrimSpace(d.Name),
		Color: normalizeContentStateColor(d.Color),
	}
}

func (c *Client) ListContentStates(ctx context.Context) ([]ContentState, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/wiki/rest/api/content-states", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create list content states request: %w", err)
	}

	var payload json.RawMessage
	if err := c.do(req, &payload); err != nil {
		return nil, fmt.Errorf("execute list content states request: %w", err)
	}
	return decodeContentStateListPayload(payload)
}

func (c *Client) ListSpaceContentStates(ctx context.Context, spaceKey string) ([]ContentState, error) {
	key := strings.TrimSpace(spaceKey)
	if key == "" {
		return nil, errors.New("space key is required")
	}

	req, err := c.newRequest(ctx, http.MethodGet, "/wiki/rest/api/space/"+url.PathEscape(key)+"/state", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create list space content states request: %w", err)
	}

	var payload json.RawMessage
	if err := c.do(req, &payload); err != nil {
		return nil, fmt.Errorf("execute list space content states request: %w", err)
	}
	return decodeContentStateListPayload(payload)
}

func (c *Client) GetAvailableContentStates(ctx context.Context, pageID string) ([]ContentState, error) {
	id := strings.TrimSpace(pageID)
	if id == "" {
		return nil, errors.New("page ID is required")
	}

	req, err := c.newRequest(ctx, http.MethodGet, "/wiki/rest/api/content/"+url.PathEscape(id)+"/state/available", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create available content states request: %w", err)
	}

	var payload json.RawMessage
	if err := c.do(req, &payload); err != nil {
		if isHTTPStatus(err, http.StatusNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("execute available content states request: %w", err)
	}
	return decodeContentStateListPayload(payload)
}

// GetContentStatus fetches the visual UI content status (lozenge) for a page via v1 API.
func (c *Client) GetContentStatus(ctx context.Context, pageID string, pageStatus string) (string, error) {
	id := strings.TrimSpace(pageID)
	if id == "" {
		return "", errors.New("page ID is required")
	}

	query := url.Values{}
	query.Set("status", normalizeContentStatePageStatus(pageStatus))

	req, err := c.newRequest(
		ctx,
		http.MethodGet,
		"/wiki/rest/api/content/"+url.PathEscape(id)+"/state",
		query,
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("create content status request: %w", err)
	}

	var result struct {
		Name         string `json:"name"`
		ContentState struct {
			Name string `json:"name"`
		} `json:"contentState"`
	}
	if err := c.do(req, &result); err != nil {
		// If 404, it might just mean no state is set
		if isHTTPStatus(err, http.StatusNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("execute content status request: %w", err)
	}

	if name := strings.TrimSpace(result.ContentState.Name); name != "" {
		return name, nil
	}
	return strings.TrimSpace(result.Name), nil
}

// SetContentStatus sets the visual UI content status (lozenge) for a page via v1 API.
func (c *Client) SetContentStatus(ctx context.Context, pageID string, pageStatus string, state ContentState) error {
	id := strings.TrimSpace(pageID)
	if id == "" {
		return errors.New("page ID is required")
	}
	statusName := strings.TrimSpace(state.Name)
	if statusName == "" {
		return errors.New("status name is required")
	}
	if state.ID <= 0 || normalizeContentStateColor(state.Color) == "" {
		if available, err := c.GetAvailableContentStates(ctx, id); err == nil {
			for _, candidate := range available {
				if strings.EqualFold(strings.TrimSpace(candidate.Name), statusName) {
					if state.ID <= 0 {
						state.ID = candidate.ID
					}
					if normalizeContentStateColor(state.Color) == "" {
						state.Color = candidate.Color
					}
					break
				}
			}
		}
	}

	query := url.Values{}
	query.Set("status", normalizeContentStatePageStatus(pageStatus))

	payload := map[string]any{
		"name": statusName,
	}
	if state.ID > 0 {
		payload["id"] = state.ID
	}
	if color := normalizeContentStateColor(state.Color); color != "" {
		payload["color"] = color
	}

	req, err := c.newRequest(
		ctx,
		http.MethodPut,
		"/wiki/rest/api/content/"+url.PathEscape(id)+"/state",
		query,
		payload,
	)
	if err != nil {
		return fmt.Errorf("create set content status request: %w", err)
	}

	var result map[string]any // Read the response but discard
	if err := c.do(req, &result); err != nil {
		return fmt.Errorf("execute set content status request: %w", err)
	}

	return nil
}

// DeleteContentStatus removes the visual UI content status (lozenge) from a page via v1 API.
func (c *Client) DeleteContentStatus(ctx context.Context, pageID string, pageStatus string) error {
	id := strings.TrimSpace(pageID)
	if id == "" {
		return errors.New("page ID is required")
	}

	query := url.Values{}
	query.Set("status", normalizeContentStatePageStatus(pageStatus))

	req, err := c.newRequest(
		ctx,
		http.MethodDelete,
		"/wiki/rest/api/content/"+url.PathEscape(id)+"/state",
		query,
		nil,
	)
	if err != nil {
		return fmt.Errorf("create delete content status request: %w", err)
	}

	if err := c.do(req, nil); err != nil {
		// 404 is fine, it means it's already deleted or doesn't exist
		if isHTTPStatus(err, http.StatusNotFound) {
			return nil
		}
		return fmt.Errorf("execute delete content status request: %w", err)
	}

	return nil
}

func normalizeContentStatePageStatus(pageStatus string) string {
	switch strings.ToLower(strings.TrimSpace(pageStatus)) {
	case "draft":
		return "draft"
	case "archived":
		return "archived"
	default:
		return "current"
	}
}

func normalizeContentStateColor(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, "#"))
	if !contentStateColorPattern.MatchString(value) {
		return ""
	}
	return strings.ToUpper(value)
}

func normalizeContentStates(stateGroups ...[]contentStateDTO) []ContentState {
	seen := map[string]struct{}{}
	out := make([]ContentState, 0)
	for _, group := range stateGroups {
		for _, item := range group {
			state := item.toModel()
			key := strings.ToLower(strings.TrimSpace(state.Name))
			if key == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, state)
		}
	}
	return out
}

func decodeContentStateListPayload(payload json.RawMessage) ([]ContentState, error) {
	if len(payload) == 0 {
		return nil, nil
	}

	var bare []contentStateDTO
	if err := json.Unmarshal(payload, &bare); err == nil {
		return normalizeContentStates(bare), nil
	}

	var wrapped struct {
		ContentStates []contentStateDTO `json:"contentStates"`
		Results       []contentStateDTO `json:"results"`
	}
	if err := json.Unmarshal(payload, &wrapped); err != nil {
		return nil, fmt.Errorf("decode content states payload: %w", err)
	}
	return normalizeContentStates(wrapped.ContentStates, wrapped.Results), nil
}

// GetLabels fetches all labels for a given page via v1 API.
func (c *Client) GetLabels(ctx context.Context, pageID string) ([]string, error) {
	id := strings.TrimSpace(pageID)
	if id == "" {
		return nil, errors.New("page ID is required")
	}

	req, err := c.newRequest(
		ctx,
		http.MethodGet,
		"/wiki/rest/api/content/"+url.PathEscape(id)+"/label",
		nil,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create get labels request: %w", err)
	}

	var result struct {
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	}

	if err := c.do(req, &result); err != nil {
		return nil, fmt.Errorf("execute get labels request: %w", err)
	}

	labels := make([]string, 0, len(result.Results))
	for _, l := range result.Results {
		labels = append(labels, l.Name)
	}

	return labels, nil
}

// AddLabels adds labels to a given page via v1 API.
func (c *Client) AddLabels(ctx context.Context, pageID string, labels []string) error {
	id := strings.TrimSpace(pageID)
	if id == "" {
		return errors.New("page ID is required")
	}
	if len(labels) == 0 {
		return nil
	}

	type labelPayload struct {
		Prefix string `json:"prefix"`
		Name   string `json:"name"`
	}

	var payload []labelPayload
	for _, l := range labels {
		payload = append(payload, labelPayload{
			Prefix: "global",
			Name:   l,
		})
	}

	req, err := c.newRequest(
		ctx,
		http.MethodPost,
		"/wiki/rest/api/content/"+url.PathEscape(id)+"/label",
		nil,
		payload,
	)
	if err != nil {
		return fmt.Errorf("create add labels request: %w", err)
	}

	var result map[string]any // Read the response but discard
	if err := c.do(req, &result); err != nil {
		return fmt.Errorf("execute add labels request: %w", err)
	}

	return nil
}

// RemoveLabel removes a specific label from a given page via v1 API.
func (c *Client) RemoveLabel(ctx context.Context, pageID string, labelName string) error {
	id := strings.TrimSpace(pageID)
	if id == "" {
		return errors.New("page ID is required")
	}
	label := strings.TrimSpace(labelName)
	if label == "" {
		return errors.New("label name is required")
	}

	req, err := c.newRequest(
		ctx,
		http.MethodDelete,
		"/wiki/rest/api/content/"+url.PathEscape(id)+"/label?name="+url.QueryEscape(label),
		nil,
		nil,
	)
	if err != nil {
		return fmt.Errorf("create remove label request: %w", err)
	}

	if err := c.do(req, nil); err != nil {
		if isHTTPStatus(err, http.StatusNotFound) {
			return nil // Label already removed
		}
		return fmt.Errorf("execute remove label request: %w", err)
	}

	return nil
}
