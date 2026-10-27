package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/johnlindquist/gmkit/internal/output"
)

func mediaCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "media",
		Short: "Download and manage attached media",
	}
	c.AddCommand(mediaDownloadCmd())
	return c
}

func mediaDownloadCmd() *cobra.Command {
	var msgID, outPath string
	c := &cobra.Command{
		Use:   "download",
		Short: "Download and decrypt the media attached to a message",
		Long: "Looks up the message by --message, fetches the encrypted bytes " +
			"from Google's CDN, decrypts them with the per-message key stored " +
			"during sync, and writes the result to disk. Defaults to " +
			"$STORE/media/<message_id>.<ext>; override with --out.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if msgID == "" {
				return fmt.Errorf("--message is required")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), daemonCallTimeout)
			defer cancel()
			var result struct {
				Path     string `json:"path"`
				Bytes    int64  `json:"bytes"`
				MimeType string `json:"mime_type"`
			}
			if err := callDaemon(ctx, "media.download", map[string]string{"message_id": msgID}, &result); err != nil {
				return err
			}
			if outPath != "" {
				path := outPath
				if info, err := os.Stat(path); err == nil && info.IsDir() {
					path = filepath.Join(path, filepath.Base(result.Path))
				}
				if path != result.Path {
					data, err := os.ReadFile(result.Path)
					if err != nil {
						return err
					}
					if err := os.WriteFile(path, data, 0600); err != nil {
						return err
					}
				}
				result.Path = path
			}
			if flags.jsonOut {
				return output.JSON(os.Stdout, map[string]any{
					"message_id": msgID, "path": result.Path, "bytes": result.Bytes, "mime_type": result.MimeType,
				})
			}
			fmt.Fprintf(os.Stderr, "Downloaded %d bytes to %s\n", result.Bytes, result.Path)
			return nil
		},
	}
	c.Flags().StringVar(&msgID, "message", "", "message_id whose media should be downloaded")
	c.Flags().StringVar(&outPath, "out", "", "output path (default: $STORE/media/<message_id><ext>)")
	return c
}
