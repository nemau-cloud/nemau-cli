package cli

import (
	"context"
	"time"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
	"github.com/spf13/cobra"
)

func newRegionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "region",
		Aliases: []string{"regions"},
		Short:   "Regions and region health",
	}

	cmd.AddCommand(&cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List the enabled regions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.Regions.List(ctx)
			if err != nil {
				return err
			}
			v := view{columns: []string{"SLUG", "NAME", "COUNTRY"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{it.Slug, it.DisplayName, it.Country})
				v.ids = append(v.ids, it.Slug)
			}
			return render(cmd, v)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use: "status <slug>", Short: "Check a region's health",
		Long: `Check a region's health.

An unhealthy region answers 503 and the command then exits non-zero, so it can
be used directly in a monitoring script.`,
		Args: requireArgs(1, "nemau region status <slug>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			st, err := c.Regions.Status(ctx, args[0])
			if err != nil {
				return err
			}
			return render(cmd, view{
				columns: []string{"REGION", "HEALTHY", "CHECKED AT"},
				rows:    [][]string{{st.Slug, yesNo(st.Healthy), st.CheckedAt}},
				data:    st, id: st.Slug,
			})
		},
	})
	return cmd
}

func newTaskCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "task",
		Aliases: []string{"tasks"},
		Short:   "Follow asynchronous operations",
		Long: `Follow asynchronous operations.

Show one task by id, or list the operations on one resource. There is no task
list for a whole project or organization.`,
	}

	cmd.AddCommand(&cobra.Command{
		Use: "get <id>", Short: "Show one task",
		Args: requireArgs(1, "nemau task get <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			task, err := c.Tasks.Get(ctx, args[0])
			if err != nil {
				return err
			}
			return render(cmd, taskView(task))
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use: "list <resource-id>", Aliases: []string{"ls"},
		Short: "List a resource's operations",
		Args:  requireArgs(1, "nemau task list <resource-id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.Tasks.ListForResource(ctx, args[0])
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "TYPE", "STATUS", "PROGRESS", "AGE"}, data: items}
			for _, it := range items {
				created := it.CreatedAt
				v.rows = append(v.rows, []string{it.ID, it.TaskType, it.Status,
					itoa(it.Progress) + " %", ago(&created)})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use: "wait <id>", Short: "Wait for a task to finish",
		Args: requireArgs(1, "nemau task wait <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			task, err := c.WaitForTask(ctx, args[0])
			if err != nil {
				return err
			}
			return render(cmd, taskView(task))
		},
	})
	return cmd
}

func taskView(t *nemau.Task) view {
	resource := "-"
	if t.ResourceID != nil {
		resource = *t.ResourceID
	}
	resourceType := "-"
	if t.ResourceType != nil {
		resourceType = *t.ResourceType
	}
	failure := "-"
	if t.Error != nil {
		failure = *t.Error
	}
	created := t.CreatedAt
	rows := [][]string{
		{"id", t.ID},
		{"type", t.TaskType},
		{"status", t.Status},
		{"progress", itoa(t.Progress) + " %"},
		{"resource", resource},
		{"resource type", resourceType},
		{"created", ago(&created)},
		{"error", failure},
	}
	if t.CompletedAt != nil {
		rows = append(rows, []string{"completed", t.CompletedAt.Format(time.RFC3339)})
	}
	return view{columns: []string{"FIELD", "VALUE"}, rows: rows, data: t, id: t.ID}
}

func newMeteringCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "metering",
		Short: "Consumption, cost and billing events",
		Long: `Consumption, cost and billing events.

The rows do not follow a stable public schema, so the output is raw JSON.`,
	}

	var q nemau.UsageQuery
	var from, to string
	addFlags := func(c *cobra.Command) *cobra.Command {
		f := c.Flags()
		f.StringVar(&q.ResourceType, "resource-type", "", "filter by resource type")
		f.StringVar(&from, "from", "", "start of the period (RFC 3339)")
		f.StringVar(&to, "to", "", "end of the period (RFC 3339)")
		f.IntVar(&q.Page, "page", 0, "page number")
		f.IntVar(&q.PageSize, "page-size", 0, "page size")
		return c
	}

	parsePeriod := func() error {
		if from != "" {
			t, err := time.Parse(time.RFC3339, from)
			if err != nil {
				return err
			}
			q.PeriodStart = t
		}
		if to != "" {
			t, err := time.Parse(time.RFC3339, to)
			if err != nil {
				return err
			}
			q.PeriodEnd = t
		}
		return nil
	}

	run := func(fn func(*nemau.Client, context.Context) (map[string]any, error)) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			if err := parsePeriod(); err != nil {
				return err
			}
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			data, err := fn(c, ctx)
			if err != nil {
				return err
			}
			forceJSON()
			return render(cmd, view{data: data})
		}
	}

	cmd.AddCommand(
		addFlags(&cobra.Command{Use: "usage", Short: "Hourly consumption",
			RunE: run(func(c *nemau.Client, ctx context.Context) (map[string]any, error) {
				return c.Metering.Usage(ctx, q)
			})}),
		addFlags(&cobra.Command{Use: "summary", Short: "Consumption totals",
			RunE: run(func(c *nemau.Client, ctx context.Context) (map[string]any, error) {
				return c.Metering.UsageSummary(ctx, q)
			})}),
		addFlags(&cobra.Command{Use: "costs", Short: "Accrued cost per resource",
			RunE: run(func(c *nemau.Client, ctx context.Context) (map[string]any, error) {
				return c.Metering.Costs(ctx, q)
			})}),
		addFlags(&cobra.Command{Use: "events", Short: "Billing events",
			RunE: run(func(c *nemau.Client, ctx context.Context) (map[string]any, error) {
				return c.Metering.Events(ctx, q)
			})}),
	)
	return cmd
}
