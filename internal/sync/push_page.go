package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func snapshotPageContent(page confluence.Page) pushContentSnapshot {
	clonedBody := append(json.RawMessage(nil), page.BodyADF...)
	return pushContentSnapshot{
		SpaceID:      strings.TrimSpace(page.SpaceID),
		Title:        strings.TrimSpace(page.Title),
		ParentPageID: strings.TrimSpace(page.ParentPageID),
		Status:       normalizePageLifecycleState(page.Status),
		BodyADF:      clonedBody,
	}
}

func restorePageContentSnapshot(ctx context.Context, remote PushRemote, pageID string, snapshot pushContentSnapshot) error {
	pageID = strings.TrimSpace(pageID)
	if pageID == "" {
		return errors.New("page ID is required")
	}

	headPage, err := remote.GetPage(ctx, pageID)
	if err != nil {
		return fmt.Errorf("fetch latest page %s: %w", pageID, err)
	}

	spaceID := strings.TrimSpace(snapshot.SpaceID)
	if spaceID == "" {
		spaceID = strings.TrimSpace(headPage.SpaceID)
	}
	if spaceID == "" {
		return fmt.Errorf("resolve space id for page %s", pageID)
	}

	parentID := strings.TrimSpace(snapshot.ParentPageID)
	title := strings.TrimSpace(snapshot.Title)
	if title == "" {
		title = strings.TrimSpace(headPage.Title)
	}
	if title == "" {
		return fmt.Errorf("resolve title for page %s", pageID)
	}

	body := append(json.RawMessage(nil), snapshot.BodyADF...)
	if len(body) == 0 {
		body = []byte(`{"version":1,"type":"doc","content":[]}`)
	}

	nextVersion := headPage.Version + 1
	if nextVersion <= 0 {
		nextVersion = 1
	}

	_, err = remote.UpdatePage(ctx, pageID, confluence.PageUpsertInput{
		SpaceID:      spaceID,
		ParentPageID: parentID,
		Title:        title,
		Status:       normalizePageLifecycleState(snapshot.Status),
		Version:      nextVersion,
		BodyADF:      body,
	})
	if err != nil {
		return fmt.Errorf("update page %s to restore snapshot: %w", pageID, err)
	}

	return nil
}

func capturePageMetadataSnapshot(ctx context.Context, remote PushRemote, pageID string) (pushMetadataSnapshot, error) {
	status, err := remote.GetContentStatus(ctx, pageID)
	if err != nil {
		return pushMetadataSnapshot{}, fmt.Errorf("get content status: %w", err)
	}

	labels, err := remote.GetLabels(ctx, pageID)
	if err != nil {
		return pushMetadataSnapshot{}, fmt.Errorf("get labels: %w", err)
	}

	return pushMetadataSnapshot{
		ContentStatus: strings.TrimSpace(status),
		Labels:        fs.NormalizeLabels(labels),
	}, nil
}

func restorePageMetadataSnapshot(ctx context.Context, remote PushRemote, pageID string, snapshot pushMetadataSnapshot) error {
	targetStatus := strings.TrimSpace(snapshot.ContentStatus)
	currentStatus, err := remote.GetContentStatus(ctx, pageID)
	if err != nil {
		return fmt.Errorf("get content status: %w", err)
	}
	currentStatus = strings.TrimSpace(currentStatus)

	if currentStatus != targetStatus {
		if targetStatus == "" {
			if err := remote.DeleteContentStatus(ctx, pageID); err != nil {
				return fmt.Errorf("delete content status: %w", err)
			}
		} else {
			if err := remote.SetContentStatus(ctx, pageID, targetStatus); err != nil {
				return fmt.Errorf("set content status: %w", err)
			}
		}
	}

	remoteLabels, err := remote.GetLabels(ctx, pageID)
	if err != nil {
		return fmt.Errorf("get labels: %w", err)
	}

	targetLabelSet := map[string]struct{}{}
	for _, label := range fs.NormalizeLabels(snapshot.Labels) {
		targetLabelSet[label] = struct{}{}
	}

	currentLabelSet := map[string]struct{}{}
	for _, label := range fs.NormalizeLabels(remoteLabels) {
		currentLabelSet[label] = struct{}{}
	}

	for label := range currentLabelSet {
		if _, keep := targetLabelSet[label]; keep {
			continue
		}
		if err := remote.RemoveLabel(ctx, pageID, label); err != nil {
			return fmt.Errorf("remove label %q: %w", label, err)
		}
	}

	toAdd := make([]string, 0)
	for label := range targetLabelSet {
		if _, exists := currentLabelSet[label]; exists {
			continue
		}
		toAdd = append(toAdd, label)
	}
	sort.Strings(toAdd)

	if len(toAdd) > 0 {
		if err := remote.AddLabels(ctx, pageID, toAdd); err != nil {
			return fmt.Errorf("add labels: %w", err)
		}
	}

	return nil
}
