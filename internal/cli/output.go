package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// view is what a command produces: a table for humans, the raw value for JSON,
// and the one identifier --quiet prints.
//
// Every command builds both shapes. A table generated from the JSON would be
// hard to read or would leave information out.
type view struct {
	columns []string
	rows    [][]string
	// data is marshalled as-is by -o json; it is the SDK value, so JSON output
	// is the API's own shape and stays usable with jq across CLI versions.
	data any
	// id is what --quiet prints. For a list, ids collects one per row.
	id  string
	ids []string
}

func render(cmd *cobra.Command, v view) error {
	out := cmd.OutOrStdout()

	if g.quiet {
		switch {
		case len(v.ids) > 0:
			for _, id := range v.ids {
				fmt.Fprintln(out, id)
			}
		case v.id != "":
			fmt.Fprintln(out, v.id)
		}
		return nil
	}

	switch strings.ToLower(g.output) {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(v.data)
	case "table", "":
		return renderTable(cmd, v)
	default:
		return fmt.Errorf("unknown output format %q (table, json)", g.output)
	}
}

func renderTable(cmd *cobra.Command, v view) error {
	out := cmd.OutOrStdout()
	if len(v.rows) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "no results")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	if len(v.columns) > 0 {
		fmt.Fprintln(w, strings.Join(v.columns, "\t"))
	}
	for _, row := range v.rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	return w.Flush()
}

// dash renders an empty value as "-" so that no table cell is left blank.
func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func itoa(n int) string { return strconv.Itoa(n) }

func i32toa(n int32) string { return strconv.FormatInt(int64(n), 10) }

// ago renders a timestamp as an age, such as "5m" or "3d".
func ago(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	d := time.Since(*t)
	switch {
	case d < time.Minute:
		return "less than 1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// joinLimit renders a slice compactly, keeping a table row on one line.
func joinLimit(values []string, max int) string {
	if len(values) == 0 {
		return "-"
	}
	if len(values) <= max {
		return strings.Join(values, ",")
	}
	return strings.Join(values[:max], ",") + fmt.Sprintf(" (+%d)", len(values)-max)
}
