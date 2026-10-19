package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
	"github.com/spf13/cobra"
)

func newComputeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "compute",
		Aliases: []string{"c"},
		Short:   "Instances, images, flavors, keypairs",
	}
	cmd.AddCommand(
		newInstanceCommand(),
		newImageCommand(),
		newFlavorCommand(),
		newKeypairCommand(),
		newSnapshotCommand(),
		newPricingCommand(),
	)
	return cmd
}

func newInstanceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "instance",
		Aliases: []string{"instances", "i"},
		Short:   "Manage compute instances",
	}
	cmd.AddCommand(
		instanceListCommand(),
		instanceGetCommand(),
		instanceCreateCommand(),
		instanceDeleteCommand(),
		instanceLifecycleCommand("start", "Start an instance", (*nemau.ComputeService).Start),
		instanceLifecycleCommand("stop", "Stop an instance", (*nemau.ComputeService).Stop),
		instanceLifecycleCommand("pause", "Pause an instance in memory", (*nemau.ComputeService).Pause),
		instanceLifecycleCommand("unpause", "Resume a paused instance", (*nemau.ComputeService).Unpause),
		instanceLifecycleCommand("suspend", "Suspend an instance to disk", (*nemau.ComputeService).Suspend),
		instanceLifecycleCommand("resume", "Resume a suspended instance", (*nemau.ComputeService).Resume),
		instanceRebootCommand(),
		instanceResizeCommand(),
		instanceSecurityGroupsCommand(),
		instanceMetricsCommand(),
		updateCommand("update <id>", "Change an instance's name or description",
			[]string{flagName, flagDescription}, "nemau compute instance update <id>", 1,
			func(c *nemau.Client, ctx context.Context, _ *updateFlags, u nemau.Update, args []string) (*nemau.TaskRef, error) {
				return c.Compute.UpdateInstance(ctx, args[0], u)
			}),
	)
	return cmd
}

func instanceListCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the project's instances",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			items, err := c.Compute.ListInstances(ctx)
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "NAME", "STATUS", "VCPU", "RAM", "DISK", "ADDRESSES", "AGE"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{
					it.ID, it.Name, it.Status,
					itoa(it.Sizing.VCPUs), fmt.Sprintf("%d MB", it.Sizing.RAMMB), fmt.Sprintf("%d GB", it.Sizing.DiskGB),
					joinLimit(it.Addresses, 2), ago(it.Created),
				})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	}
}

func instanceGetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show one instance",
		Args:  requireArgs(1, "nemau compute instance get <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			it, err := c.Compute.GetInstance(ctx, args[0])
			if err != nil {
				return err
			}
			groups := make([]string, 0, len(it.SecurityGroups))
			for _, sg := range it.SecurityGroups {
				groups = append(groups, sg.Name)
			}
			image, flavor := "-", "-"
			if it.Image != nil {
				image = dash(it.Image.Name)
			}
			if it.Flavor != nil {
				flavor = dash(it.Flavor.Name)
			}
			return render(cmd, view{
				columns: []string{"FIELD", "VALUE"},
				rows: [][]string{
					{"id", it.ID},
					{"name", it.Name},
					{"description", dash(it.Description)},
					{"status", it.Status},
					{"flavor", flavor},
					{"size", fmt.Sprintf("%d vCPU, %d MB, %d GB", it.Sizing.VCPUs, it.Sizing.RAMMB, it.Sizing.DiskGB)},
					{"image", image},
					{"ssh key", dash(it.KeyName)},
					{"addresses", joinLimit(it.Addresses, 8)},
					{"security groups", joinLimit(groups, 8)},
					{"created", ago(it.Created)},
				},
				data: it,
				id:   it.ID,
			})
		},
	}
}

func instanceCreateCommand() *cobra.Command {
	var req nemau.CreateInstanceRequest
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an instance",
		Long: `Create an instance.

The size is either --flavor or the three --vcpus, --ram and --disk values: the
API rejects a partial custom size. --flavor additionally requires the
"compute-flavors" organization feature.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			// Checked here rather than left to the API so the message names the
			// flags the user typed instead of the JSON fields they did not.
			if req.FlavorID == "" && (req.VCPUs == 0 || req.RAMMB == 0 || req.DiskGB == 0) {
				return fmt.Errorf("need --flavor, or all three of --vcpus, --ram and --disk")
			}
			ref, err := c.Compute.CreateInstance(ctx, req)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "instance_id")
		},
	}
	f := cmd.Flags()
	f.StringVar(&req.Name, "name", "", "instance name (required)")
	f.StringVar(&req.Description, "description", "", "free-form description")
	f.StringVar(&req.ImageID, "image", "", "image id (required)")
	f.StringVar(&req.FlavorID, "flavor", "", "flavor id")
	f.IntVar(&req.VCPUs, "vcpus", 0, "number of vCPUs (1-64)")
	f.IntVar(&req.RAMMB, "ram", 0, "memory in MB (1024-262144)")
	f.IntVar(&req.DiskGB, "disk", 0, "disk in GB (10-5000)")
	f.StringVar(&req.ServiceLevel, "service-level", "", "service level")
	f.StringVar(&req.NetworkID, "network", "", "network to attach")
	f.StringSliceVar(&req.SecurityGroups, "security-group", nil, "security group (repeatable)")
	f.StringVar(&req.KeyName, "key-name", "", "SSH keypair name")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("image")
	return cmd
}

func instanceDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete an instance",
		Args:    requireArgs(1, "nemau compute instance delete <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Compute.DeleteInstance(ctx, args[0])
			if err != nil {
				return err
			}
			return withInUseHint(
				finish(cmd, ctx, ref, "instance_id"),
				"detach the volume first: nemau storage volume detach <volume-id> <instance-id> (nemau storage volume list shows the ids)",
			)
		},
	}
}

// instanceLifecycleCommand builds the power verbs, which all share one shape:
// one id, no options, one task.
func instanceLifecycleCommand(name, short string, action func(*nemau.ComputeService, context.Context, string) (*nemau.TaskRef, error)) *cobra.Command {
	return &cobra.Command{
		Use:   name + " <id>",
		Short: short,
		Args:  requireArgs(1, "nemau compute instance "+name+" <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := action(c.Compute, ctx, args[0])
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "instance_id")
		},
	}
}

func instanceRebootCommand() *cobra.Command {
	var hard bool
	cmd := &cobra.Command{
		Use:   "reboot <id>",
		Short: "Reboot an instance",
		Args:  requireArgs(1, "nemau compute instance reboot <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			kind := "SOFT"
			if hard {
				kind = "HARD"
			}
			ref, err := c.Compute.Reboot(ctx, args[0], kind)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "instance_id")
		},
	}
	cmd.Flags().BoolVar(&hard, "hard", false, "hard reset (power cut) instead of a clean shutdown")
	return cmd
}

func instanceResizeCommand() *cobra.Command {
	var sizing nemau.Sizing
	cmd := &cobra.Command{
		Use:   "resize <id>",
		Short: "Resize an instance",
		Long: `Resize an instance.

All three values are required: the API does not accept a partial resize.
Bounds: 1-64 vCPUs, 1024-262144 MB, 10-5000 GB.

Raising only the disk is applied online and the instance keeps running. Changing
vCPUs or memory power-cycles it, because without hotplug the hypervisor would
otherwise leave the change pending until the next boot.`,
		Args: requireArgs(1, "nemau compute instance resize <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Compute.Resize(ctx, args[0], sizing)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "instance_id")
		},
	}
	f := cmd.Flags()
	f.IntVar(&sizing.VCPUs, "vcpus", 0, "number of vCPUs (required)")
	f.IntVar(&sizing.RAMMB, "ram", 0, "memory in MB (required)")
	f.IntVar(&sizing.DiskGB, "disk", 0, "disk in GB (required)")
	_ = cmd.MarkFlagRequired("vcpus")
	_ = cmd.MarkFlagRequired("ram")
	_ = cmd.MarkFlagRequired("disk")
	return cmd
}

func instanceSecurityGroupsCommand() *cobra.Command {
	var groups []string
	var clear bool
	cmd := &cobra.Command{
		Use:   "set-security-groups <id>",
		Short: "Replace an instance's security groups",
		Long: `Replace an instance's security groups.

The given list replaces the instance's current groups instead of adding to
them. --clear sends an empty list, which stops filtering the instance's traffic
without deleting the instance.`,
		Args: requireArgs(1, "nemau compute instance set-security-groups <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if clear && len(groups) > 0 {
				return fmt.Errorf("--clear and --security-group are mutually exclusive")
			}
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			if clear {
				groups = []string{}
			}
			ref, err := c.Compute.SetSecurityGroups(ctx, args[0], groups)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "instance_id")
		},
	}
	cmd.Flags().StringSliceVar(&groups, "security-group", nil, "security group (repeatable)")
	cmd.Flags().BoolVar(&clear, "clear", false, "remove every security group")
	return cmd
}

func instanceMetricsCommand() *cobra.Command {
	var period string
	cmd := &cobra.Command{
		Use:   "metrics <id>",
		Short: "An instance's metrics",
		Args:  requireArgs(1, "nemau compute instance metrics <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			data, err := c.Compute.InstanceMetrics(ctx, args[0], period)
			if err != nil {
				return err
			}
			// Time series have no useful table form, so the output is JSON.
			forceJSON()
			return render(cmd, view{data: data, id: args[0]})
		},
	}
	cmd.Flags().StringVar(&period, "period", "1h", "period: 1h, 6h, 24h, 7d")
	return cmd
}

func newImageCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "image",
		Aliases: []string{"images"},
		Short:   "List the bootable images",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.Compute.ListImages(ctx)
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "NAME", "OS", "STATUS", "FORMAT"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{it.ID, it.Name, dash(it.OSName), dash(it.Status), dash(it.DiskFormat)})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	}
}

func newFlavorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "flavor",
		Aliases: []string{"flavors"},
		Short:   "Manage instance flavors",
		Long: `Manage instance flavors.

These commands require the "compute-flavors" organization feature; without it
the API answers 403.`,
	}

	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the flavors",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.Compute.ListFlavors(ctx)
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "NAME", "VCPU", "RAM", "DISK", "SLA"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{it.ID, it.Name, itoa(it.VCPUs),
					fmt.Sprintf("%d MB", it.RAMMB), fmt.Sprintf("%d GB", it.DiskGB), dash(it.ServiceLevel)})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	}

	var req nemau.FlavorRequest
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a flavor",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			// Synchronous, unlike most writes: the flavor is a catalogue row.
			fl, err := c.Compute.CreateFlavor(ctx, req)
			if err != nil {
				return err
			}
			return render(cmd, view{
				columns: []string{"ID", "NAME", "VCPU", "RAM", "DISK"},
				rows:    [][]string{{fl.ID, fl.Name, itoa(fl.VCPUs), itoa(fl.RAMMB), itoa(fl.DiskGB)}},
				data:    fl, id: fl.ID,
			})
		},
	}
	cf := create.Flags()
	cf.StringVar(&req.Name, "name", "", "flavor name (required)")
	cf.IntVar(&req.VCPUs, "vcpus", 0, "number of vCPUs (required)")
	cf.IntVar(&req.RAMMB, "ram", 0, "memory in MB (required)")
	cf.IntVar(&req.DiskGB, "disk", 0, "disk in GB (required)")
	cf.StringVar(&req.ServiceLevel, "service-level", "", "service level")
	for _, name := range []string{"name", "vcpus", "ram", "disk"} {
		_ = create.MarkFlagRequired(name)
	}

	del := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a flavor",
		Args:    requireArgs(1, "nemau compute flavor delete <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			if err := c.Compute.DeleteFlavor(ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "flavor deleted:", args[0])
			return nil
		},
	}

	cmd.AddCommand(list, create, del)
	return cmd
}

func newKeypairCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "keypair",
		Aliases: []string{"keypairs", "key"},
		Short:   "Manage SSH keypairs",
	}

	cmd.AddCommand(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the keypairs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.Compute.ListKeypairs(ctx)
			if err != nil {
				return err
			}
			v := view{columns: []string{"NAME", "TYPE", "FINGERPRINT"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{it.Name, dash(it.Type), dash(it.Fingerprint)})
				v.ids = append(v.ids, it.Name)
			}
			return render(cmd, v)
		},
	})

	var publicKeyFile, privateOut string
	create := &cobra.Command{
		Use:   "create <name>",
		Short: "Register a public key, or generate one",
		Long: `Register a public key, or generate one.

Without --public-key-file, the platform generates an Ed25519 pair and returns
the private key once: it is never stored and cannot be fetched again. Use
--private-key-out to write it to disk.`,
		Args: requireArgs(1, "nemau compute keypair create <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			publicKey := ""
			if publicKeyFile != "" {
				raw, err := os.ReadFile(publicKeyFile)
				if err != nil {
					return fmt.Errorf("reading %s: %w", publicKeyFile, err)
				}
				publicKey = string(raw)
			}

			kp, err := c.Compute.CreateKeypair(ctx, args[0], publicKey)
			if err != nil {
				return err
			}
			if kp.PrivateKey != "" && privateOut != "" {
				// 0600 before anything else touches it: this is the only copy.
				if err := os.WriteFile(privateOut, []byte(kp.PrivateKey), 0o600); err != nil {
					return fmt.Errorf("writing the private key to %s: %w", privateOut, err)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "private key written to %s (mode 0600)\n", privateOut)
			} else if kp.PrivateKey != "" {
				fmt.Fprintln(cmd.OutOrStdout(), kp.PrivateKey)
				fmt.Fprintln(cmd.ErrOrStderr(), "private key shown once: it is not stored")
			}
			return render(cmd, view{
				columns: []string{"NAME", "PUBLIC KEY"},
				rows:    [][]string{{kp.Name, dash(kp.PublicKey)}},
				data:    kp, id: kp.Name,
			})
		},
	}
	create.Flags().StringVar(&publicKeyFile, "public-key-file", "", "public key file to register")
	create.Flags().StringVar(&privateOut, "private-key-out", "", "write the generated private key to this file")

	del := &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm"},
		Short:   "Delete a keypair",
		Args:    requireArgs(1, "nemau compute keypair delete <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Compute.DeleteKeypair(ctx, args[0])
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "keypair_id")
		},
	}

	cmd.AddCommand(create, del)
	return cmd
}

func newSnapshotCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "snapshot",
		Aliases: []string{"snapshots", "snap"},
		Short:   "Manage instance snapshots",
		Long: `A snapshot copies an instance's system disk to the object store, without
stopping it. Attached block volumes are never included. It is billed on the
bytes actually stored (the compressed archive), per GB and month.

Snapshots whose storage is "hypervisor" predate the object store: they can be
listed and deleted, are not billed, and cannot be restored.

Deleting an instance deletes its snapshots.`,
	}

	var instanceID string
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the snapshots",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.Compute.ListSnapshots(ctx, instanceID)
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "NAME", "INSTANCE", "STATUS", "STORAGE", "SIZE", "MONTHLY", "AGE"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{
					it.ID, it.Name, it.InstanceID, dash(it.Status), dash(it.Storage),
					snapshotSize(it), snapshotMonthly(it), ago(it.CreatedAt),
				})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	}
	list.Flags().StringVar(&instanceID, "instance", "", "list only this instance's snapshots")

	create := &cobra.Command{
		Use:   "create <instance-id> <name>",
		Short: "Create a snapshot",
		Long: `Create a snapshot of an instance's system disk, without stopping it.

The operation lasts as long as the disk takes to reach the object store: minutes,
or hours for a large disk. If that can outlast --timeout, raise it (--timeout 0
waits indefinitely) or pass --wait=false and follow the snapshot in "list".`,
		Args: requireArgs(2, "nemau compute snapshot create <instance-id> <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Compute.CreateSnapshot(ctx, args[0], args[1])
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "snapshot_id")
		},
	}

	del := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a snapshot",
		Args:    requireArgs(1, "nemau compute snapshot delete <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Compute.DeleteSnapshot(ctx, args[0])
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "snapshot_id")
		},
	}

	restore := &cobra.Command{
		Use:   "restore <id>",
		Short: "Restore a snapshot onto its instance",
		Long: `Restore a snapshot onto the instance it was taken from.

The data is restored beside the running instance first, then its system disk is
swapped: the instance stops only for the swap and starts again if it was running.
Everything written to the disk since the snapshot is lost. Addresses, security
groups and attached volumes are kept, and the disk keeps its current size.`,
		Args: requireArgs(1, "nemau compute snapshot restore <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Compute.RestoreSnapshot(ctx, args[0])
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "instance_id")
		},
	}

	cmd.AddCommand(list, create, del, restore)
	return cmd
}

// snapshotSize renders what the object store holds for a snapshot. It prints a
// dash while the snapshot is being created and for hypervisor snapshots: the
// size is not known there, and a zero would read as free.
func snapshotSize(s nemau.Snapshot) string {
	if s.SizeBytes == nil {
		return "-"
	}
	return humanBytes(*s.SizeBytes)
}

// snapshotMonthly renders the monthly price of keeping a snapshot, excluding VAT.
func snapshotMonthly(s nemau.Snapshot) string {
	if s.MonthlyCost == nil {
		return "-"
	}
	currency := strings.ToUpper(s.Currency)
	if currency == "" {
		currency = "EUR"
	}
	// A small snapshot costs a fraction of a cent; "0.00" would read as free.
	if cost := *s.MonthlyCost; cost > 0 && cost < 0.01 {
		return "<0.01 " + currency
	}
	return fmt.Sprintf("%.2f %s", *s.MonthlyCost, currency)
}

func newPricingCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "pricing",
		Short: "Show the region's price list",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			pricing, err := c.Compute.GetPricing(ctx)
			if err != nil {
				return err
			}
			forceJSON()
			return render(cmd, view{data: pricing})
		},
	}
}
