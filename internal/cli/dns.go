package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
	"github.com/spf13/cobra"
)

func newDNSCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "dns", Short: "DNS zones and records"}
	cmd.AddCommand(dnsZoneCommand(), dnsRecordCommand())
	return cmd
}

func dnsZoneCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "zone", Aliases: []string{"zones"}, Short: "Manage DNS zones",
		Long: `Manage DNS zones.

A new zone is not served until you prove that you control its domain. It is
created PENDING_VERIFICATION, with a TXT record to publish at the domain's
current DNS host ("nemau dns zone get" shows it). Once the record is visible,
"nemau dns zone verify" checks it and activates the zone; record sets can only
be written to an active zone.

A <zone> argument is the zone's id (a UUID) or its name ("example.com" or
"example.com."). A name is looked up in the zone list and must match exactly
one zone.`,
	}

	cmd.AddCommand(&cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List the zones",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.DNS.ListZones(ctx)
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "NAME", "CONTACT", "TTL", "STATUS"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{it.ID, it.Name, dash(it.Email), itoa(it.TTL), dash(it.Status)})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	})

	var req nemau.CreateZoneRequest
	create := &cobra.Command{
		Use: "create <name>", Short: "Create a zone",
		Args: requireArgs(1, "nemau dns zone create <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			req.Name = args[0]
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.DNS.CreateZone(ctx, req)
			if err != nil {
				return err
			}
			id, err := finishID(cmd, ctx, ref, "zone_id")
			if err != nil || g.quiet {
				return err
			}
			if id == "" {
				// --wait=false: the zone does not exist yet, so neither does
				// the record to publish.
				fmt.Fprintf(cmd.ErrOrStderr(),
					"the zone is created pending verification; once the task is done, \"nemau dns zone get %s\" shows the TXT record to publish\n",
					args[0])
				return nil
			}
			zone, err := c.DNS.GetZone(ctx, id)
			if err != nil {
				return err
			}
			printZoneVerification(cmd, zone)
			return nil
		},
	}
	create.Flags().StringVar(&req.Email, "email", "", "the zone's SOA contact (required)")
	create.Flags().StringVar(&req.Description, "description", "", "free-form description")
	create.Flags().IntVar(&req.TTL, "ttl", 0, "default TTL in seconds (default: 300)")
	_ = create.MarkFlagRequired("email")

	cmd.AddCommand(updateCommand("update <zone>", "Change a zone's description, contact or TTL",
		[]string{flagDescription, flagEmail, flagTTL}, "nemau dns zone update <zone>", 1,
		func(c *nemau.Client, ctx context.Context, _ *updateFlags, u nemau.Update, args []string) (*nemau.TaskRef, error) {
			id, err := zoneID(ctx, c, args[0])
			if err != nil {
				return nil, err
			}
			return c.DNS.UpdateZone(ctx, id, u)
		}))

	cmd.AddCommand(&cobra.Command{
		Use: "get <zone>", Short: "Show one zone and, while pending, the record that proves its ownership",
		Args: requireArgs(1, "nemau dns zone get <zone>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			zone, err := resolveZone(ctx, c, args[0])
			if err != nil {
				return err
			}
			if err := render(cmd, zoneView(zone)); err != nil {
				return err
			}
			if !g.quiet {
				printZoneVerification(cmd, zone)
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "verify <zone>",
		Short: "Check the TXT record that proves ownership of a pending zone",
		Long: `Check the TXT record that proves ownership of a pending zone.

The platform looks the record up and activates the zone if it holds the expected
value. While the record is absent or wrong the operation fails and the zone stays
pending: a record published a moment ago may take a while to reach the domain's
nameservers, so check it with "dig TXT <record name> +short" and run this again.`,
		Args: requireArgs(1, "nemau dns zone verify <zone>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			id, err := zoneID(ctx, c, args[0])
			if err != nil {
				return err
			}
			ref, err := c.DNS.VerifyZone(ctx, id)
			if err != nil {
				return err
			}
			if err := finish(cmd, ctx, ref, "zone_id"); err != nil {
				return verifyHint(err, args[0])
			}
			if g.wait && !g.quiet {
				fmt.Fprintf(cmd.ErrOrStderr(), "zone %s is verified and active\n", args[0])
			}
			return nil
		},
	})

	cmd.AddCommand(create, &cobra.Command{
		Use: "delete <zone>", Aliases: []string{"rm"}, Short: "Delete a zone",
		Args: requireArgs(1, "nemau dns zone delete <zone>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			id, err := zoneID(ctx, c, args[0])
			if err != nil {
				return err
			}
			ref, err := c.DNS.DeleteZone(ctx, id)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "zone_id")
		},
	})
	return cmd
}

func dnsRecordCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "record", Aliases: []string{"records", "recordset"},
		Short: "Manage record sets",
		Long: `Manage record sets.

A record set's id is "canonical.name./TYPE", for example "www.example.com./A".
<zone> is the zone's id or its name, as for "nemau dns zone".`,
	}

	cmd.AddCommand(&cobra.Command{
		Use: "list <zone>", Aliases: []string{"ls"}, Short: "List a zone's record sets",
		Args: requireArgs(1, "nemau dns record list <zone>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			id, err := zoneID(ctx, c, args[0])
			if err != nil {
				return err
			}
			items, err := c.DNS.ListRecordSets(ctx, id)
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "NAME", "TYPE", "TTL", "VALUES", "STATUS"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{it.ID, it.Name, it.Type, itoa(it.TTL),
					joinLimit(it.Records, 3), dash(it.Status)})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	})

	var req nemau.CreateRecordSetRequest
	create := &cobra.Command{
		Use: "create <zone> <name>", Short: "Create a record set",
		Args: requireArgs(2, "nemau dns record create <zone> <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			req.Name = args[1]
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			id, err := zoneID(ctx, c, args[0])
			if err != nil {
				return err
			}
			ref, err := c.DNS.CreateRecordSet(ctx, id, req)
			if err != nil {
				return dnsZoneHint(err, args[0])
			}
			return finish(cmd, ctx, ref, "recordset_id")
		},
	}
	create.Flags().StringVar(&req.Type, "type", "", "record type: A, AAAA, CNAME, MX, TXT… (required)")
	create.Flags().StringSliceVar(&req.Records, "record", nil, "record value (repeatable, required)")
	create.Flags().IntVar(&req.TTL, "ttl", 0, "TTL in seconds (default: 300)")
	create.Flags().StringVar(&req.Description, "description", "", "free-form description")
	_ = create.MarkFlagRequired("type")
	_ = create.MarkFlagRequired("record")

	var update nemau.UpdateRecordSetRequest
	updateCmd := &cobra.Command{
		Use: "update <zone> <recordset-id>", Short: "Update a record set",
		Args: requireArgs(2, "nemau dns record update <zone> <recordset-id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			id, err := zoneID(ctx, c, args[0])
			if err != nil {
				return err
			}
			ref, err := c.DNS.UpdateRecordSet(ctx, id, args[1], update)
			if err != nil {
				return dnsZoneHint(err, args[0])
			}
			return finish(cmd, ctx, ref, "recordset_id")
		},
	}
	updateCmd.Flags().StringSliceVar(&update.Records, "record", nil, "record value (repeatable)")
	updateCmd.Flags().IntVar(&update.TTL, "ttl", 0, "TTL in seconds")
	updateCmd.Flags().StringVar(&update.Description, "description", "", "free-form description")

	cmd.AddCommand(create, updateCmd, &cobra.Command{
		Use: "delete <zone> <recordset-id>", Aliases: []string{"rm"},
		Short: "Delete a record set",
		Args:  requireArgs(2, "nemau dns record delete <zone> <recordset-id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			id, err := zoneID(ctx, c, args[0])
			if err != nil {
				return err
			}
			ref, err := c.DNS.DeleteRecordSet(ctx, id, args[1])
			if err != nil {
				return dnsZoneHint(err, args[0])
			}
			return finish(cmd, ctx, ref, "recordset_id")
		},
	})
	return cmd
}

// zoneView is the detail of one zone. While the zone is pending, it carries the
// record that proves ownership of the domain.
func zoneView(zone *nemau.DNSZone) view {
	rows := [][]string{
		{"id", zone.ID},
		{"name", zone.Name},
		{"description", dash(zone.Description)},
		{"contact", dash(zone.Email)},
		{"ttl", itoa(zone.TTL)},
		{"status", dash(zone.Status)},
	}
	if v := zone.Verification; v != nil {
		rows = append(rows,
			[]string{"verification record", v.Name},
			[]string{"verification type", v.Type},
			[]string{"verification value", v.Value},
		)
	}
	rows = append(rows, []string{"created", dash(zone.CreatedAt)})
	return view{columns: []string{"FIELD", "VALUE"}, rows: rows, data: zone, id: zone.ID}
}

// printZoneVerification tells, on stderr, what to publish for a pending zone
// and what to run next. It says nothing about an active zone.
func printZoneVerification(cmd *cobra.Command, zone *nemau.DNSZone) {
	v := zone.Verification
	if v == nil {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(), `
zone %s is pending verification. Publish this record at the domain's current DNS host:

  %s  %s  %q

then run: nemau dns zone verify %s
`, zone.Name, v.Name, v.Type, v.Value, zone.ID)
}

// dnsZoneHint adds the next step to a record-set write refused because the zone
// is still pending verification.
func dnsZoneHint(err error, zoneID string) error {
	if nemau.HasReason(err, nemau.ReasonDNSZonePendingVerification) {
		return fmt.Errorf("%w\nprove ownership of the zone first: \"nemau dns zone get %s\" shows the TXT record to publish, \"nemau dns zone verify %s\" checks it", err, zoneID, zoneID)
	}
	return err
}

// verifyHint explains a failed verification: the record is absent or wrong, or
// has not propagated yet.
func verifyHint(err error, zoneID string) error {
	var failed *nemau.TaskFailedError
	if errors.As(err, &failed) {
		return fmt.Errorf("%w\nthe zone stays pending: check that the TXT record shown by \"nemau dns zone get %s\" is published, then run this again once it has propagated", err, zoneID)
	}
	return err
}

// uuidPattern recognizes a zone id. A zone's id is the platform's UUID; its
// name ("example.com.") is not an identifier, so anything else is looked up.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// zoneID turns a <zone> argument into the zone's id. A UUID is taken as is,
// without a request; a name is resolved through the zone list.
func zoneID(ctx context.Context, c *nemau.Client, ref string) (string, error) {
	if uuidPattern.MatchString(ref) {
		return ref, nil
	}
	zone, err := resolveZone(ctx, c, ref)
	if err != nil {
		return "", err
	}
	return zone.ID, nil
}

// resolveZone finds the zone a <zone> argument names: by id first, then by
// name, with or without the trailing dot and in any case. A name matching
// several zones is refused rather than guessed.
func resolveZone(ctx context.Context, c *nemau.Client, ref string) (*nemau.DNSZone, error) {
	zones, err := c.DNS.ListZones(ctx)
	if err != nil {
		return nil, err
	}
	for i := range zones {
		if zones[i].ID == ref {
			return &zones[i], nil
		}
	}
	want := canonicalZoneName(ref)
	var matches []*nemau.DNSZone
	for i := range zones {
		if canonicalZoneName(zones[i].Name) == want {
			matches = append(matches, &zones[i])
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no DNS zone with id or name %q in this project (see \"nemau dns zone list\")", ref)
	case 1:
		return matches[0], nil
	}
	ids := make([]string, 0, len(matches))
	for _, zone := range matches {
		ids = append(ids, fmt.Sprintf("%s (%s)", zone.ID, dash(zone.Status)))
	}
	return nil, fmt.Errorf("%d DNS zones are named %s: give the id of the one you mean: %s",
		len(matches), want, strings.Join(ids, ", "))
}

// canonicalZoneName is a zone name as the platform stores it: lower case,
// rooted with a trailing dot.
func canonicalZoneName(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), ".")) + "."
}
