// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/stacklok/go-microvm/image"
)

func cacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage the OCI image cache",
	}
	cmd.AddCommand(cacheListCmd())
	cmd.AddCommand(cacheGCCmd())
	cmd.AddCommand(cachePurgeCmd())
	return cmd
}

func cacheListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show cached images",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			dir, err := imageCacheDir()
			if err != nil {
				return fmt.Errorf("resolving image cache directory: %w", err)
			}

			cache := image.NewCache(dir)
			entries, err := cache.List()
			if err != nil {
				return fmt.Errorf("listing cache: %w", err)
			}

			if len(entries) == 0 {
				fmt.Println("No cached images")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "DIGEST\tSIZE\tLAST USED\tIMAGE")

			var totalRootfs int64
			orphans := 0
			for _, e := range entries {
				digest := shortDigest(e.Digest)
				size := humanSize(e.Size)
				age := timeAgo(e.ModTime)
				ref := "(orphan)"
				if len(e.Refs) > 0 {
					ref = e.Refs[0]
					if len(e.Refs) > 1 {
						ref += fmt.Sprintf(" (+%d more)", len(e.Refs)-1)
					}
				} else {
					orphans++
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", digest, size, age, ref)
				totalRootfs += e.Size
			}
			_ = w.Flush()

			// Summary line.
			layerSize, _ := cache.LayerCache().Size()
			total := totalRootfs + layerSize

			fmt.Println()
			summary := fmt.Sprintf("Entries: %d", len(entries))
			if orphans > 0 {
				summary += fmt.Sprintf(" (%d orphan)", orphans)
			}
			summary += fmt.Sprintf(" | Rootfs: %s", humanSize(totalRootfs))
			if layerSize > 0 {
				summary += fmt.Sprintf(" | Layers: %s", humanSize(layerSize))
			}
			summary += fmt.Sprintf(" | Total: %s", humanSize(total))
			fmt.Println(summary)

			return nil
		},
	}
}

func cacheGCCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Remove unreferenced cache entries",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			dir, err := imageCacheDir()
			if err != nil {
				return fmt.Errorf("resolving image cache directory: %w", err)
			}

			cache := image.NewCache(dir)

			if dryRun {
				entries, err := cache.List()
				if err != nil {
					return fmt.Errorf("listing cache: %w", err)
				}
				var count int
				var totalSize int64
				for _, e := range entries {
					if len(e.Refs) == 0 {
						fmt.Printf("would remove %s (%s)\n", shortDigest(e.Digest), humanSize(e.Size))
						count++
						totalSize += e.Size
					}
				}
				if count == 0 {
					fmt.Println("No unreferenced entries to remove")
				} else {
					fmt.Printf("\n%d entries, %s would be freed\n", count, humanSize(totalSize))
				}
				return nil
			}

			removed, err := cache.GC()
			if err != nil {
				return fmt.Errorf("cache gc: %w", err)
			}
			if removed == 0 {
				fmt.Println("No unreferenced entries to remove")
			} else {
				fmt.Printf("Removed %d unreferenced cache entries\n", removed)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be removed without removing it")
	return cmd
}

func cachePurgeCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Remove all cached images",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			dir, err := imageCacheDir()
			if err != nil {
				return fmt.Errorf("resolving image cache directory: %w", err)
			}

			cache := image.NewCache(dir)

			if !force {
				fmt.Fprintf(os.Stderr, "This will remove all cached images at %s\n", dir)
				fmt.Fprint(os.Stderr, "Continue? [y/N] ")
				var answer string
				fmt.Scanln(&answer)
				if answer != "y" && answer != "Y" {
					fmt.Println("Aborted")
					return nil
				}
			}

			if err := cache.Purge(); err != nil {
				return fmt.Errorf("purging cache: %w", err)
			}
			fmt.Println("Cache purged")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")
	return cmd
}

// shortDigest truncates a digest to a readable length.
// "sha256:abc123def456..." → "sha256:abc123def456"
func shortDigest(digest string) string {
	const maxHexLen = 12
	parts := splitDigest(digest)
	if parts[1] != "" && len(parts[1]) > maxHexLen {
		return parts[0] + ":" + parts[1][:maxHexLen]
	}
	return digest
}

// splitDigest splits "sha256:hex" into ["sha256", "hex"].
func splitDigest(digest string) [2]string {
	for i, c := range digest {
		if c == ':' {
			return [2]string{digest[:i], digest[i+1:]}
		}
	}
	return [2]string{digest, ""}
}

// humanSize formats a byte count as a human-readable string.
func humanSize(b int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// timeAgo formats a time.Time as a relative duration string.
func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}
