package confluence

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

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
