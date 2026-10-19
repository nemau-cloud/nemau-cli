package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
	"github.com/spf13/cobra"
)

func newStorageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "storage",
		Aliases: []string{"s"},
		Short:   "Block volumes and object storage",
	}
	cmd.AddCommand(volumeCommand(), containerCommand(), objectCommand(), exportCommand(), credentialsCommand())
	return cmd
}

func volumeCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "volume", Aliases: []string{"volumes", "vol"}, Short: "Manage block volumes"}

	cmd.AddCommand(&cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List the volumes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.Storage.ListVolumes(ctx)
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "NAME", "SIZE", "STATUS", "ATTACHED TO"}, data: items}
			for _, it := range items {
				attached := "-"
				if len(it.Attachments) > 0 {
					attached = dash(it.Attachments[0].ServerName)
					if attached == "-" {
						attached = it.Attachments[0].ServerID
					}
				}
				v.rows = append(v.rows, []string{it.ID, it.Name, fmt.Sprintf("%d GB", it.Size), dash(it.Status), attached})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	})

	var size int
	var description string
	create := &cobra.Command{
		Use: "create <name>", Short: "Create a volume",
		Args: requireArgs(1, "nemau storage volume create <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Storage.CreateVolume(ctx, args[0], description, size)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "volume_id")
		},
	}
	create.Flags().IntVar(&size, "size", 0, "size in GB (required)")
	create.Flags().StringVar(&description, "description", "", "free-form description")
	_ = create.MarkFlagRequired("size")

	var newSize int
	extend := &cobra.Command{
		Use: "extend <id>", Short: "Extend a volume",
		Long: `Extend a volume.

A volume only ever grows: there is no shrink operation.`,
		Args: requireArgs(1, "nemau storage volume extend <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Storage.ExtendVolume(ctx, args[0], newSize)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "volume_id")
		},
	}
	extend.Flags().IntVar(&newSize, "size", 0, "new size in GB (required)")
	_ = extend.MarkFlagRequired("size")

	cmd.AddCommand(updateCommand("update <id>", "Change a volume's name or description",
		[]string{flagName, flagDescription}, "nemau storage volume update <id>", 1,
		func(c *nemau.Client, ctx context.Context, _ *updateFlags, u nemau.Update, args []string) (*nemau.TaskRef, error) {
			return c.Storage.UpdateVolume(ctx, args[0], u)
		}))

	cmd.AddCommand(create, extend,
		&cobra.Command{
			Use: "attach <volume-id> <instance-id>", Short: "Attach a volume to an instance",
			Args: requireArgs(2, "nemau storage volume attach <volume-id> <instance-id>"),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, ctx, cancel, err := runCtx(cmd)
				if err != nil {
					return err
				}
				defer cancel()
				ref, err := c.Storage.AttachVolume(ctx, args[0], args[1])
				if err != nil {
					return err
				}
				return finish(cmd, ctx, ref, "volume_id")
			},
		},
		&cobra.Command{
			Use: "detach <volume-id> <instance-id>", Short: "Detach a volume from an instance",
			Args: requireArgs(2, "nemau storage volume detach <volume-id> <instance-id>"),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, ctx, cancel, err := runCtx(cmd)
				if err != nil {
					return err
				}
				defer cancel()
				ref, err := c.Storage.DetachVolume(ctx, args[0], args[1])
				if err != nil {
					return err
				}
				return finish(cmd, ctx, ref, "volume_id")
			},
		},
		&cobra.Command{
			Use: "delete <id>", Aliases: []string{"rm"}, Short: "Delete a volume",
			Args: requireArgs(1, "nemau storage volume delete <id>"),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, ctx, cancel, err := runCtx(cmd)
				if err != nil {
					return err
				}
				defer cancel()
				ref, err := c.Storage.DeleteVolume(ctx, args[0])
				if err != nil {
					return err
				}
				return finish(cmd, ctx, ref, "volume_id")
			},
		})
	return cmd
}

func containerCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "container", Aliases: []string{"containers", "bucket"}, Short: "Manage object containers"}

	cmd.AddCommand(&cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List the containers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.Storage.ListContainers(ctx)
			if err != nil {
				return err
			}
			v := view{columns: []string{"NAME", "S3 BUCKET", "OBJECTS", "BYTES", "PROTECTION"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{it.Name, dash(it.S3Bucket), fmt.Sprintf("%d", it.Count),
					humanBytes(it.Bytes), describeProtection(it.Protection)})
				v.ids = append(v.ids, it.Name)
			}
			return render(cmd, v)
		},
	})

	var request nemau.CreateContainerRequest
	create := &cobra.Command{
		Use: "create <name>", Short: "Create a container",
		Long: `Create a container.

--object-lock makes stored versions immutable for the retention period. This
protects backups from ransomware: even a compromised key cannot erase what is
already written.

These options can only be set at creation. S3 does not allow object locking on
an existing bucket, so a container for immutable backups must be created with
it.

Retention modes:
  GOVERNANCE   the platform can lift the lock (default)
  COMPLIANCE   nobody can lift the lock before it expires, the platform included`,
		Args: requireArgs(1, "nemau storage container create <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			request.Name = args[0]
			ref, err := c.Storage.CreateContainerWithOptions(ctx, request)
			if err != nil {
				return err
			}
			// COMPLIANCE mode cannot be undone, so warn once at creation.
			if request.RetentionMode == nemau.RetentionCompliance && !g.quiet {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"COMPLIANCE mode: objects will be un-erasable for %d day(s), including by the platform\n",
					request.RetentionDays)
			}
			return finish(cmd, ctx, ref, "container_id")
		},
	}
	cf := create.Flags()
	cf.BoolVar(&request.Versioning, "versioning", false, "keep previous versions of objects")
	cf.BoolVar(&request.ObjectLock, "object-lock", false, "make versions immutable (implies --versioning)")
	cf.StringVar(&request.RetentionMode, "retention-mode", "", "GOVERNANCE (default) or COMPLIANCE")
	cf.IntVar(&request.RetentionDays, "retention-days", 0, "how long objects stay immutable, in days")

	protection := &cobra.Command{
		Use: "protection <name>", Short: "Check the protection actually enforced",
		Long: `Check the protection actually enforced.

The container listing reports what was requested at creation; this command asks
the object store and reports what it enforces. Use it to verify immutability,
for example during an audit.`,
		Args: requireArgs(1, "nemau storage container protection <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			state, err := c.Storage.ContainerProtection(ctx, args[0])
			if err != nil {
				return err
			}
			rows := [][]string{
				{"versioning", dash(state.Versioning)},
				{"object lock", yesNo(state.ObjectLock)},
				{"immutable", yesNo(state.Immutable())},
			}
			if state.RetentionMode != "" {
				rows = append(rows, []string{"retention mode", state.RetentionMode})
			}
			if state.RetentionDays > 0 {
				rows = append(rows, []string{"retention", itoa(state.RetentionDays) + " day(s)"})
			}
			return render(cmd, view{
				columns: []string{"FIELD", "VALUE"},
				rows:    rows, data: state, id: args[0],
			})
		},
	}

	cmd.AddCommand(updateCommand("update <name>", "Change a container's versioning or retention",
		[]string{flagVersioning, flagRetentionDays}, "nemau storage container update <name>", 1,
		func(c *nemau.Client, ctx context.Context, _ *updateFlags, u nemau.Update, args []string) (*nemau.TaskRef, error) {
			return c.Storage.UpdateContainer(ctx, args[0], u)
		}))

	cmd.AddCommand(create, protection,
		&cobra.Command{
			Use: "delete <name>", Aliases: []string{"rm"}, Short: "Delete a container",
			Args: requireArgs(1, "nemau storage container delete <name>"),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, ctx, cancel, err := runCtx(cmd)
				if err != nil {
					return err
				}
				defer cancel()
				ref, err := c.Storage.DeleteContainer(ctx, args[0])
				if err != nil {
					return err
				}
				return finish(cmd, ctx, ref, "container_id")
			},
		})
	return cmd
}

func objectCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "object", Aliases: []string{"objects", "obj"}, Short: "Manage the objects in a container"}

	var prefix, marker string
	var limit int
	list := &cobra.Command{
		Use: "list <container>", Aliases: []string{"ls"}, Short: "List the objects in a container",
		Long: `List the objects in a container.

The listing follows the platform's page cursor until the container is exhausted,
so a large container takes several requests. --limit stops after that many
objects; the command then prints the cursor to pass to --marker to continue.
The cursor is opaque: pass it back unchanged.`,
		Args: requireArgs(1, "nemau storage object list <container>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 {
				return errors.New("--limit must not be negative")
			}
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			listing, err := collectObjects(ctx, c, args[0], prefix, marker, limit)
			if err != nil {
				return err
			}
			v := view{columns: []string{"KEY", "SIZE", "TYPE", "MODIFIED"}, data: listing}
			for _, o := range listing.Objects {
				modified := o.LastModified
				v.rows = append(v.rows, []string{o.Name, humanBytes(o.Bytes), dash(o.ContentType), ago(&modified)})
				v.ids = append(v.ids, o.Name)
			}
			if err := render(cmd, v); err != nil {
				return err
			}
			// Say that the listing stopped at --limit, so that a truncated listing
			// is not mistaken for the whole container.
			if listing.NextMarker != "" && !g.quiet && g.output != "json" {
				fmt.Fprintf(cmd.ErrOrStderr(), "listing stopped at --limit; continue with --marker %s\n", listing.NextMarker)
			}
			return nil
		},
	}
	lf := list.Flags()
	lf.StringVar(&prefix, "prefix", "", "list only the keys starting with this prefix")
	lf.StringVar(&marker, "marker", "", "resume a listing from the cursor printed when it stopped")
	lf.IntVar(&limit, "limit", 0, "stop after this many objects (default: list them all)")

	var contentType string
	put := &cobra.Command{
		Use: "put <container> <key> <file>", Short: "Upload an object",
		Long: `Upload an object.

Use "-" as the file to read standard input; the content is then buffered in
memory, because the API requires an exact Content-Length. The limit is 100 MiB
per object.`,
		Args: requireArgs(3, "nemau storage object put <container> <key> <file>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			body, size, closeFn, err := openUpload(args[2])
			if err != nil {
				return err
			}
			defer closeFn()

			obj, err := c.Storage.UploadObject(ctx, args[0], args[1], contentType, body, size)
			if err != nil {
				return err
			}
			return render(cmd, view{
				columns: []string{"KEY", "SIZE"},
				rows:    [][]string{{obj.ObjectName, humanBytes(obj.Size)}},
				data:    obj, id: obj.ObjectName,
			})
		},
	}
	put.Flags().StringVar(&contentType, "content-type", "", "MIME type (default: application/octet-stream)")

	get := &cobra.Command{
		Use: "get <container> <key> [file]", Short: "Download an object",
		Long: `Download an object.

Without a destination file, the content goes to standard output.`,
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			body, err := c.Storage.DownloadObject(ctx, args[0], args[1])
			if err != nil {
				return err
			}
			defer body.Close()

			dst := cmd.OutOrStdout()
			if len(args) == 3 {
				f, err := os.Create(args[2])
				if err != nil {
					return err
				}
				defer f.Close()
				dst = f
			}
			_, err = io.Copy(dst, body)
			return err
		},
	}

	rm := &cobra.Command{
		Use: "delete <container> <key>", Aliases: []string{"rm"}, Short: "Delete an object",
		Args: requireArgs(2, "nemau storage object delete <container> <key>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			if err := c.Storage.DeleteObject(ctx, args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "object deleted:", args[1])
			return nil
		},
	}

	cmd.AddCommand(list, put, get, copyCommand(), rm, presignCommand())
	return cmd
}

// openUpload returns a body and its exact length.
//
// The API requires a Content-Length, so a stream of unknown size has to be
// materialised. A file is streamed from disk; only stdin is buffered, and the
// 100 MiB cap bounds what that can cost.
func openUpload(path string) (io.Reader, int64, func(), error) {
	if path == "-" {
		buf, err := io.ReadAll(io.LimitReader(os.Stdin, maxUploadBytes+1))
		if err != nil {
			return nil, 0, func() {}, err
		}
		if int64(len(buf)) > maxUploadBytes {
			return nil, 0, func() {}, fmt.Errorf("object exceeds the 100 MiB limit")
		}
		return newBytesReader(buf), int64(len(buf)), func() {}, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, 0, func() {}, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, func() {}, err
	}
	if info.Size() > maxUploadBytes {
		f.Close()
		return nil, 0, func() {}, fmt.Errorf("%s is %s, over the 100 MiB limit", path, humanBytes(info.Size()))
	}
	return f, info.Size(), func() { f.Close() }, nil
}

// describeProtection renders a container's protection in one table cell.
//
// It reports the state requested at creation, which is what the listing
// carries. `nemau storage container protection <name>` asks the store what it
// actually enforces.
func describeProtection(protection *nemau.ContainerProtection) string {
	if protection == nil {
		return "-"
	}
	switch {
	case protection.ObjectLock && protection.RetentionDays > 0:
		mode := protection.RetentionMode
		if mode == "" {
			mode = nemau.RetentionGovernance
		}
		return fmt.Sprintf("immutable %s %dd", mode, protection.RetentionDays)
	case protection.ObjectLock:
		return "immutable"
	case protection.Versioning == "Enabled":
		return "versioned"
	default:
		return "-"
	}
}

// collectObjects walks a container listing page by page, following the cursor.
//
// limit is the most objects to return, 0 for all of them. The last page is
// asked for exactly what is still wanted, so a listing cut by limit ends on a
// page boundary and its NextMarker is a cursor issued by the platform, which a
// later --marker can resume from. The cursor is opaque: it is passed back as
// received and never built or edited here.
func collectObjects(ctx context.Context, c *nemau.Client, container, prefix, marker string, limit int) (*nemau.ObjectPage, error) {
	out := &nemau.ObjectPage{}
	for {
		size := nemau.MaxListObjectsLimit
		if limit > 0 && limit-len(out.Objects) < size {
			size = limit - len(out.Objects)
		}
		page, err := c.Storage.ListObjects(ctx, container, prefix, marker, size)
		if err != nil {
			return nil, err
		}
		out.Objects = append(out.Objects, page.Objects...)
		out.Count = len(out.Objects)
		out.NextMarker = page.NextMarker
		if page.NextMarker == "" || (limit > 0 && len(out.Objects) >= limit) {
			return out, nil
		}
		// A cursor that does not move would repeat a page for ever.
		if page.NextMarker == marker {
			return nil, errors.New("object listing did not advance (repeated cursor)")
		}
		marker = page.NextMarker
	}
}
