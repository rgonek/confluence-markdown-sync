package confluence

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

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
func (c *Client) SetContentStatus(ctx context.Context, pageID string, pageStatus string, statusName string) error {
	id := strings.TrimSpace(pageID)
	if id == "" {
		return errors.New("page ID is required")
	}

	query := url.Values{}
	query.Set("status", normalizeContentStatePageStatus(pageStatus))

	payload := struct {
		ContentState struct {
			Name string `json:"name"`
		} `json:"contentState"`
	}{
		ContentState: struct {
			Name string `json:"name"`
		}{
			Name: statusName,
		},
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
