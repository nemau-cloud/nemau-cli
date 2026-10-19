package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
	"github.com/spf13/cobra"
)

// presignCommand issues one URL, for scripting and for pipelines that do their
// own transfer.
func presignCommand() *cobra.Command {
	var method string
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:   "presign <container> <key>",
		Short: "Get a direct URL to an object, bypassing the API",
		Long: `Get a direct URL to an object, bypassing the API.

The URL works with any HTTP client:

    curl -o file "$(nemau storage object presign photos holiday.jpg -q)"

It bypasses the API for the transfer itself: no 100 MiB cap, and no extra hop
on every byte.

It is a bearer token for that object until it expires. Do not put it in a log
or in a shared environment variable.`,
		Args: requireArgs(2, "nemau storage object presign <container> <key>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			presign := c.Storage.PresignGet
			if strings.EqualFold(method, "PUT") {
				presign = c.Storage.PresignPut
			}
			presigned, err := presign(ctx, args[0], args[1], ttl)
			if err != nil {
				return err
			}
			return render(cmd, view{
				columns: []string{"METHOD", "EXPIRES AT", "URL"},
				rows:    [][]string{{presigned.Method, presigned.ExpiresAt.Format(time.RFC3339), presigned.URL}},
				data:    presigned,
				// --quiet prints the URL alone, so it can be piped into curl.
				id: presigned.URL,
			})
		},
	}
	cmd.Flags().StringVar(&method, "method", "GET", "GET to download, PUT to upload")
	cmd.Flags().DurationVar(&ttl, "expires-in", 0, "how long it stays valid (platform default: 15m, cap: 1h)")
	return cmd
}

// exportCommand downloads a whole container.
//
// It is the tool for taking a container's data out of the platform. Objects
// are fetched through signed URLs, so the API's per-object limit does not
// apply.
func exportCommand() *cobra.Command {
	var parallel int
	var prefix string
	var overwrite bool
	cmd := &cobra.Command{
		Use:   "export <container> <directory>",
		Short: "Download everything in a container",
		Long: `Download everything in a container.

Every object is fetched through a signed URL, straight from the store: the
100 MiB per-object cap does not apply, and the bytes never cross the API.

The key hierarchy is reproduced on disk. By default a file that is already
present with the right size is skipped, so an export resumes after an
interruption.`,
		Args: requireArgs(2, "nemau storage export <container> <directory>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			container, destination := args[0], args[1]
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return err
			}
			if parallel < 1 {
				parallel = 1
			}

			objects, err := listAllObjects(ctx, c, container, prefix)
			if err != nil {
				return err
			}
			if len(objects) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "empty container")
				return nil
			}

			progress := cmd.ErrOrStderr()
			fmt.Fprintf(progress, "%d object(s) to fetch into %s\n", len(objects), destination)

			var downloaded, skipped, failed int64
			var firstErr error
			var errOnce sync.Once

			work := make(chan nemau.Object)
			var wg sync.WaitGroup
			for range parallel {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for object := range work {
						status, err := exportObject(ctx, c, container, destination, object, overwrite)
						switch {
						case err != nil:
							atomic.AddInt64(&failed, 1)
							errOnce.Do(func() { firstErr = err })
							fmt.Fprintf(progress, "failed %s: %v\n", object.Name, err)
						case status == exportSkipped:
							atomic.AddInt64(&skipped, 1)
						default:
							atomic.AddInt64(&downloaded, 1)
						}
					}
				}()
			}

			for _, object := range objects {
				select {
				case <-ctx.Done():
				case work <- object:
					continue
				}
				break
			}
			close(work)
			wg.Wait()

			fmt.Fprintf(progress, "%d downloaded, %d skipped, %d failed\n",
				downloaded, skipped, failed)
			if failed > 0 {
				// Non-zero exit: an export that lost objects must not look like
				// a success to the script that called it.
				return fmt.Errorf("%d object(s) not fetched, first error: %w", failed, firstErr)
			}
			return ctx.Err()
		},
	}
	cmd.Flags().IntVar(&parallel, "parallel", 4, "number of concurrent downloads")
	cmd.Flags().StringVar(&prefix, "prefix", "", "fetch only the keys starting with this prefix")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "rewrite files that are already present")
	return cmd
}

type exportStatus int

const (
	exportWritten exportStatus = iota
	exportSkipped
)

// listAllObjects collects every object of a container under prefix.
//
// The API pages by cursor, so a single call returns one page only. The SDK's
// iterator follows the cursor to the end, so no object is left out.
func listAllObjects(ctx context.Context, c *nemau.Client, container, prefix string) ([]nemau.Object, error) {
	var all []nemau.Object
	for object, err := range c.Storage.IterateObjects(ctx, container, prefix) {
		if err != nil {
			return nil, err
		}
		all = append(all, object)
	}
	return all, nil
}

func exportObject(ctx context.Context, c *nemau.Client, container, destination string, object nemau.Object, overwrite bool) (exportStatus, error) {
	target, err := safeJoin(destination, object.Name)
	if err != nil {
		return exportWritten, err
	}

	if !overwrite {
		if info, err := os.Stat(target); err == nil && info.Size() == object.Bytes {
			return exportSkipped, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return exportWritten, err
	}

	presigned, err := c.Storage.PresignGet(ctx, container, object.Name, 0)
	if err != nil {
		return exportWritten, err
	}
	body, _, err := c.FetchPresigned(ctx, presigned)
	if err != nil {
		return exportWritten, err
	}
	defer body.Close()

	// Written to a temporary neighbour then renamed: an interrupted transfer
	// must not leave a short file that the next run's size check accepts as
	// complete.
	temp := target + ".part"
	file, err := os.Create(temp)
	if err != nil {
		return exportWritten, err
	}
	if _, err := io.Copy(file, body); err != nil {
		file.Close()
		_ = os.Remove(temp)
		return exportWritten, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return exportWritten, err
	}
	if err := os.Rename(temp, target); err != nil {
		_ = os.Remove(temp)
		return exportWritten, err
	}
	return exportWritten, nil
}

// safeJoin maps an object key to a path inside destination and refuses any key
// that would escape it.
//
// Object keys are arbitrary strings chosen by whoever uploaded them, and this
// command writes them to disk: a key such as "../../.ssh/authorized_keys" must
// not lead to a write outside the export directory.
//
// Such keys are refused rather than sanitised. Collapsing "../a.txt" to "a.txt"
// would let two distinct objects land on the same file, one overwriting the
// other. A refused key is reported and counted as a failure, and the export
// exits non-zero.
func safeJoin(destination, key string) (string, error) {
	// Checked before any cleaning, because cleaning would hide the traversal.
	// Both separators are examined: S3 keys are slash-separated, but
	// filepath.FromSlash leaves a literal backslash intact, and on Windows that
	// backslash is a separator.
	for _, segment := range strings.FieldsFunc(key, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if segment == ".." {
			return "", errors.New("key would escape the destination directory: " + key)
		}
	}

	// A leading slash is accepted and anchored at the destination: S3 keys may
	// start with one, and it refers to the container's root.
	cleaned := filepath.Clean(filepath.FromSlash("/" + key))
	target := filepath.Join(destination, cleaned)

	// Second check, on the absolute path, in case a later change to the code
	// above lets a traversal through.
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if absTarget != absDestination &&
		!strings.HasPrefix(absTarget, absDestination+string(os.PathSeparator)) {
		return "", errors.New("key would escape the destination directory: " + key)
	}
	return target, nil
}
