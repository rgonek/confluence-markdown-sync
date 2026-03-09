package confluence

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

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
	FileID    string `json:"fileId"`
	Title     string `json:"title"`
	Filename  string `json:"filename"`
	MediaType string `json:"mediaType"`
	Links     struct {
		WebUI    string `json:"webui"`
		Download string `json:"download"`
	} `json:"_links"`
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
				FileID:    strings.TrimSpace(item.FileID),
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

	slog.Debug("http request", "method", downloadReq.Method, "url", downloadReq.URL.String()) //nolint:gosec // Safe log of request URL

	resp, err := c.downloadClient.Do(downloadReq) //nolint:gosec // Intended SSRF for downloading user's content
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
		FileID:    strings.TrimSpace(item.FileID),
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
