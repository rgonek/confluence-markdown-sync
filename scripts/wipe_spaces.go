package main

import (
	"context"
	"fmt"
	"os"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

func main() {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Printf("Failed to load config: %v\n", err)
		os.Exit(1)
	}

	client, err := confluence.NewClient(confluence.ClientConfig{
		BaseURL:  cfg.Domain,
		Email:    cfg.Email,
		APIToken: cfg.APIToken,
	})
	if err != nil {
		fmt.Printf("Failed to create client: %v\n", err)
		os.Exit(1)
	}

	spaces := []string{"TD", "SD"}
	ctx := context.Background()

	for _, spaceKey := range spaces {
		fmt.Printf("\n=== Wiping space %s ===\n", spaceKey)
		space, err := client.GetSpace(ctx, spaceKey)
		if err != nil {
			fmt.Printf("Failed to get space %s: %v\n", spaceKey, err)
			continue
		}

		for {
			pages, err := client.ListPages(ctx, confluence.PageListOptions{
				SpaceID: space.ID,
				Limit:   100,
				Status:  "current",
			})
			if err != nil {
				fmt.Printf("Failed to list pages in %s: %v\n", spaceKey, err)
				break
			}
			if len(pages.Pages) == 0 {
				fmt.Println("No more pages in current status.")
				break
			}

			for _, page := range pages.Pages {
				fmt.Printf("Deleting page: %s (%s)\n", page.Title, page.ID)
				// Try deleting (purge=true)
				err = client.DeletePage(ctx, page.ID, true)
				if err != nil {
					fmt.Printf("Purge failed for %s, trying regular delete: %v\n", page.ID, err)
					err = client.DeletePage(ctx, page.ID, false)
					if err != nil {
						fmt.Printf("Delete failed for %s: %v\n", page.ID, err)
					}
				}
			}
		}

		// Also check drafts
		for {
			pages, err := client.ListPages(ctx, confluence.PageListOptions{
				SpaceID: space.ID,
				Limit:   100,
				Status:  "draft",
			})
			if err != nil {
				break
			}
			if len(pages.Pages) == 0 {
				break
			}
			for _, page := range pages.Pages {
				fmt.Printf("Deleting draft: %s (%s)\n", page.Title, page.ID)
				_ = client.DeletePage(ctx, page.ID, true)
			}
		}
	}
	fmt.Println("\nDeep wipe complete.")
}
