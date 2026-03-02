package confluence

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
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
		msg = confluenceStatusHint(e.StatusCode)
	}
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	if msg == "" {
		msg = "request failed"
	}
	return fmt.Sprintf("%s %s: status %d: %s", e.Method, e.URL, e.StatusCode, msg)
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
	if strings.Contains(combined, "unable to restore content") {
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

	// Check for a known error code first and return an enriched description.
	for _, codeKey := range []string{"code", "errorKey", "status"} {
		if v, ok := payload[codeKey].(string); ok {
			if hint := mapConfluenceErrorCode(v); hint != "" {
				return hint
			}
		}
	}

	for _, key := range []string{"message", "error", "reason"} {
		if v, ok := payload[key].(string); ok {
			// Try to enrich a terse message via the code mapper.
			if hint := mapConfluenceErrorCode(v); hint != "" {
				return hint
			}
			return v
		}
	}

	if msg := decodeErrorsFieldMessage(payload["errors"]); msg != "" {
		if hint := mapConfluenceErrorCode(msg); hint != "" {
			return hint
		}
		return msg
	}

	if data, ok := payload["data"].(map[string]any); ok {
		if msg := decodeErrorsFieldMessage(data["errors"]); msg != "" {
			if hint := mapConfluenceErrorCode(msg); hint != "" {
				return hint
			}
			return msg
		}
	}

	return ""
}

func decodeErrorsFieldMessage(value any) string {
	switch v := value.(type) {
	case []any:
		if len(v) == 0 {
			return ""
		}
		return decodeErrorItemMessage(v[0])
	case map[string]any:
		if msg := decodeErrorItemMessage(v); msg != "" {
			return msg
		}
		for _, child := range v {
			if msg := decodeErrorsFieldMessage(child); msg != "" {
				return msg
			}
		}
	}
	return ""
}

func decodeErrorItemMessage(value any) string {
	switch item := value.(type) {
	case string:
		return strings.TrimSpace(item)
	case map[string]any:
		title := ""
		if v, ok := item["title"].(string); ok {
			title = strings.TrimSpace(v)
		}
		detail := ""
		if v, ok := item["detail"].(string); ok {
			detail = strings.TrimSpace(v)
		}
		message := ""
		if v, ok := item["message"].(string); ok {
			message = strings.TrimSpace(v)
		}

		if title != "" && detail != "" {
			return title + ": " + detail
		}
		if title != "" {
			return title
		}
		if message != "" {
			return message
		}
		if detail != "" {
			return detail
		}
	}
	return ""
}

// confluenceStatusHint returns a Confluence-specific human-readable hint for
// common HTTP status codes where the default http.StatusText is too generic.
func confluenceStatusHint(code int) string {
	switch code {
	case http.StatusUnauthorized:
		return "authentication failed — check ATLASSIAN_API_TOKEN and ATLASSIAN_USER_EMAIL"
	case http.StatusForbidden:
		return "permission denied — the API token may lack write access to this space"
	case http.StatusConflict:
		return "version conflict — another edit was published since your last pull; run `conf pull` first"
	case http.StatusUnprocessableEntity:
		return "the page content was rejected by Confluence — check for unsupported macros or invalid ADF"
	case http.StatusTooManyRequests:
		return "rate limited by Confluence — reduce --rate-limit-rps or wait before retrying"
	case http.StatusServiceUnavailable:
		return "Confluence is temporarily unavailable — retry after a short wait"
	case http.StatusRequestEntityTooLarge:
		return "request payload too large — consider splitting large attachments"
	}
	return ""
}

// mapConfluenceErrorCode maps known Confluence API error codes/titles to
// more descriptive human-readable explanations.
func mapConfluenceErrorCode(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "INVALID_IMAGE":
		return "invalid or inaccessible image reference (the image URL may be broken or the file type unsupported)"
	case "MACRO_NOT_FOUND", "MACRONOTFOUND":
		return "unrecognized Confluence macro — the macro may not be installed in this Confluence instance"
	case "INVALID_REQUEST_PARAMETER":
		return "one or more request parameters are invalid — verify page IDs, space keys, and content"
	case "PERMISSION_DENIED":
		return "permission denied — check that the API token has the required space permissions"
	case "TITLE_ALREADY_EXISTS":
		return "a page with this title already exists in the space — choose a unique title"
	case "PARENT_PAGE_NOT_FOUND":
		return "the specified parent page does not exist or is not accessible"
	case "CONTENT_STALE":
		return "page content is stale — a newer version exists on Confluence; run `conf pull` to refresh"
	}
	return ""
}
