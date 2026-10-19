package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
	"github.com/spf13/cobra"
)

// copyCommand copies one object inside the store.
func copyCommand() *cobra.Command {
	var replaceMetadata bool
	var contentType string
	var metadata []string
	cmd := &cobra.Command{
		Use:   "copy <container> <key> <destination-container> [destination-key]",
		Short: "Copy an object, server-side",
		Long: `Copy an object, server-side.

The store copies the bytes itself: nothing goes through the API or this
machine, so the 100 MiB upload cap does not apply. The limit is 5 GiB per
object, that of one S3 copy request; beyond it, create an S3 key with
"nemau storage credentials create" and copy with rclone or the aws cli.

The destination key defaults to the source key. Both containers must belong to
the current project.

Metadata and content type are copied as they are. --replace-metadata replaces
them instead: the user metadata becomes exactly the --metadata pairs (none if
there are none), and the content type becomes --content-type, or stays the
source's. Copying an object onto itself is only accepted with
--replace-metadata: it is how metadata is rewritten in place.

A copy into a container under object lock is locked with that container's
default retention, whatever the source's.`,
		Example: `  nemau storage object copy photos 2026/holiday.jpg archive
  nemau storage object copy photos a.jpg photos b.jpg
  nemau storage object copy photos a.jpg photos a.jpg --replace-metadata --metadata owner=ops`,
		Args: cobra.RangeArgs(3, 4),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !replaceMetadata && (contentType != "" || len(metadata) > 0) {
				return errors.New("--metadata and --content-type require --replace-metadata")
			}
			pairs, err := parseMetadata(metadata)
			if err != nil {
				return err
			}
			request := nemau.CopyObjectRequest{
				Container:            args[0],
				Key:                  args[1],
				DestinationContainer: args[2],
				ReplaceMetadata:      replaceMetadata,
				ContentType:          contentType,
				Metadata:             pairs,
			}
			if len(args) == 4 {
				request.DestinationKey = args[3]
			}

			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			copied, err := c.Storage.CopyObject(ctx, request)
			if err != nil {
				return err
			}

			columns := []string{"CONTAINER", "KEY", "SIZE"}
			row := []string{copied.Container, copied.Key, humanBytes(copied.Size)}
			if copied.Retention != nil {
				columns = append(columns, "LOCKED UNTIL")
				row = append(row, copied.Retention.RetainUntil.Format(time.RFC3339)+" ("+copied.Retention.Mode+")")
			}
			return render(cmd, view{columns: columns, rows: [][]string{row}, data: copied, id: copied.Key})
		},
	}
	f := cmd.Flags()
	f.BoolVar(&replaceMetadata, "replace-metadata", false, "replace the source's metadata instead of copying it")
	f.StringArrayVar(&metadata, "metadata", nil, "user metadata as key=value, repeatable (requires --replace-metadata)")
	f.StringVar(&contentType, "content-type", "", "content type of the copy (requires --replace-metadata; default: the source's)")
	return cmd
}

// parseMetadata reads key=value pairs. The value may itself contain "=".
func parseMetadata(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("--metadata %q: expected key=value", pair)
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("--metadata %q: key given twice", key)
		}
		out[key] = value
	}
	return out, nil
}
