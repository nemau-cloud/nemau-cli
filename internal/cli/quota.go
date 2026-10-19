package cli

import (
	"fmt"
	"sort"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
	"github.com/spf13/cobra"
)

// newQuotaCommand reports the capacity limits that writes are checked against.
func newQuotaCommand() *cobra.Command {
	var allScopes bool

	cmd := &cobra.Command{
		Use:     "quota",
		Aliases: []string{"quotas"},
		Short:   "Show the capacity limits applied to the target project",
		Long: `Show the capacity limits applied to the target project.

A write is admitted only when both the project and the organization that owns
it have room. Each dimension gets one row, for the scope that will refuse
first: the one with the least headroom, which is not always the smallest
limit. Use --all-scopes to see every scope.

AVAILABLE subtracts pending reservations: capacity claimed by operations that
have been admitted but have not finished. That is what the platform compares a
new request against.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			quotas, err := c.Quotas.Get(ctx)
			if err != nil {
				return err
			}

			v := view{columns: []string{"DIMENSION", "SCOPE", "USED", "RESERVED", "LIMIT", "AVAILABLE"}, data: quotas}
			for _, row := range quotaRows(quotas, allScopes) {
				v.rows = append(v.rows, []string{
					row.name, row.scope,
					quotaAmount(row.dimension.Used, row.dimension.Unit),
					quotaAmount(row.dimension.Reserved, row.dimension.Unit),
					quotaAmount(row.dimension.Limit, row.dimension.Unit),
					quotaAmount(row.dimension.Available(), row.dimension.Unit),
				})
			}
			return render(cmd, v)
		},
	}
	cmd.Flags().BoolVar(&allScopes, "all-scopes", false, "show every scope, not only the one that binds")
	return cmd
}

type quotaRow struct {
	name      string
	scope     string
	dimension nemau.QuotaDimension
}

// quotaRows flattens the reply into the rows to print, dimensions in a stable
// order so two runs can be diffed.
func quotaRows(quotas *nemau.QuotaList, allScopes bool) []quotaRow {
	names := map[string]bool{}
	for _, scope := range quotas.Quotas {
		for _, dimension := range scope.Dimensions {
			names[dimension.Name] = true
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)

	rows := make([]quotaRow, 0, len(ordered))
	for _, name := range ordered {
		if allScopes {
			for i := range quotas.Quotas {
				if dimension := quotas.Quotas[i].Dimension(name); dimension != nil {
					rows = append(rows, quotaRow{name: name, scope: quotas.Quotas[i].Scope, dimension: *dimension})
				}
			}
			continue
		}
		if scope, binding := bindingScope(quotas, name); binding != nil {
			rows = append(rows, quotaRow{name: name, scope: scope, dimension: *binding})
		}
	}
	return rows
}

// bindingScope returns the scope that will refuse first and its dimension.
//
// The SDK's Binding returns only the dimension, and the table also needs the
// scope, so the walk is repeated here.
func bindingScope(quotas *nemau.QuotaList, name string) (string, *nemau.QuotaDimension) {
	var scope string
	var binding *nemau.QuotaDimension
	for i := range quotas.Quotas {
		dimension := quotas.Quotas[i].Dimension(name)
		if dimension == nil {
			continue
		}
		if binding == nil || dimension.Available() < binding.Available() {
			scope, binding = quotas.Quotas[i].Scope, dimension
		}
	}
	return scope, binding
}

// quotaAmount prints a value in the unit the API counted it in. Bytes get
// binary units; MB and GB are labelled rather than converted, because the
// limit is expressed in them and a converted number no longer matches the one
// the platform enforces.
func quotaAmount(value int64, unit string) string {
	switch unit {
	case "bytes":
		return humanBytes(value)
	case "count", "":
		return fmt.Sprintf("%d", value)
	default:
		return fmt.Sprintf("%d %s", value, unit)
	}
}
