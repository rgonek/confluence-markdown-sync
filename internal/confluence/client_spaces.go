package confluence

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type spaceDTO struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
	Type string `json:"type"`
}

func (s spaceDTO) toModel() Space {
	return Space(s)
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
