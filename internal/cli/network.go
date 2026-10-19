package cli

import (
	"context"
	"errors"
	"fmt"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
	"github.com/spf13/cobra"
)

func newNetworkCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "network",
		Aliases: []string{"net"},
		Short:   "Networks, subnets, floating IPs, security groups",
	}
	cmd.AddCommand(
		networkCRUDCommand(),
		subnetCommand(),
		interfaceCommand(),
		floatingIPCommand(),
		securityGroupCommand(),
		capabilitiesCommand(),
	)
	return cmd
}

// ipVersionLabel names an IP version for a table cell. A region that predates
// IPv6 omits the field, which means IPv4.
func ipVersionLabel(version int) string {
	if version == nemau.IPv6 {
		return "IPv6"
	}
	return "IPv4"
}

// checkIPVersion rejects anything but 4 and 6 before a request is made.
func checkIPVersion(version int) error {
	if version != nemau.IPv4 && version != nemau.IPv6 {
		return fmt.Errorf("--ip-version must be 4 or 6, not %d", version)
	}
	return nil
}

func capabilitiesCommand() *cobra.Command {
	return &cobra.Command{
		Use: "capabilities", Short: "Show which optional network features the region offers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			caps, err := c.Network.GetCapabilities(ctx)
			if err != nil {
				return err
			}
			return render(cmd, view{columns: []string{"FIELD", "VALUE"}, data: caps, rows: [][]string{
				{"IPv6 floating IPs", yesNo(caps.IPv6FloatingIPs)},
				{"IPv6 subnets", yesNo(caps.IPv6Subnets)},
			}})
		},
	}
}

func networkCRUDCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "vpc", Aliases: []string{"networks"}, Short: "Manage virtual networks"}

	cmd.AddCommand(&cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List the networks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.Network.ListNetworks(ctx)
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "NAME", "STATUS", "EXTERNAL"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{it.ID, it.Name, dash(it.Status), yesNo(it.External)})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	})

	var description string
	create := &cobra.Command{
		Use: "create <name>", Short: "Create a network",
		Args: requireArgs(1, "nemau network vpc create <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Network.CreateNetwork(ctx, args[0], description)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "network_id")
		},
	}
	create.Flags().StringVar(&description, "description", "", "free-form description")

	cmd.AddCommand(updateCommand("update <id>", "Change a network's name or description",
		[]string{flagName, flagDescription}, "nemau network vpc update <id>", 1,
		func(c *nemau.Client, ctx context.Context, _ *updateFlags, u nemau.Update, args []string) (*nemau.TaskRef, error) {
			return c.Network.UpdateNetwork(ctx, args[0], u)
		}))

	cmd.AddCommand(create, &cobra.Command{
		Use: "delete <id>", Aliases: []string{"rm"}, Short: "Delete a network",
		Args: requireArgs(1, "nemau network vpc delete <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Network.DeleteNetwork(ctx, args[0])
			if err != nil {
				return err
			}
			return withInUseHint(
				finish(cmd, ctx, ref, "network_id"),
				"delete what sits on the network first: nemau network subnet list, nemau compute instance list and nemau load-balancer instance list show it",
			)
		},
	})
	return cmd
}

// subnetOverlapHint keeps the API's message for an overlapping range, which
// names the conflicting subnet, and adds the command that lists the ranges in
// use. The conflict may be on another network of the project, so the command
// lists the whole project.
func subnetOverlapHint(err error) error {
	var apiErr *nemau.APIError
	if !nemau.IsSubnetOverlap(err) || !errors.As(err, &apiErr) || apiErr.Message == "" {
		return err
	}
	return fmt.Errorf(`%s; "nemau network subnet list" shows the ranges in use`, apiErr.Message)
}

func subnetCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "subnet", Aliases: []string{"subnets"}, Short: "Manage subnets"}

	cmd.AddCommand(&cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List the subnets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.Network.ListSubnets(ctx)
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "NAME", "NETWORK", "IP", "CIDR", "GATEWAY", "DNS"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{it.ID, it.Name, it.NetworkID, ipVersionLabel(it.IPVersion), it.CIDR,
					dash(it.GatewayIP), joinLimit(it.DNSServers, 2)})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	})

	var cidr, gateway string
	var dnsServers []string
	var ipVersion int
	create := &cobra.Command{
		Use: "create <network-id> <name>", Short: "Create a subnet",
		Long: "Create a subnet. An IPv4 subnet needs --cidr. An IPv6 subnet (--ip-version 6)\n" +
			"takes no range: the platform assigns a /64 from the region's routed prefix\n" +
			"and its first address as the gateway. The network must already have an IPv4\n" +
			"subnet, and instances created on it afterwards need a security group.",
		Args: requireArgs(2, "nemau network subnet create <network-id> <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkIPVersion(ipVersion); err != nil {
				return err
			}
			switch {
			case ipVersion == nemau.IPv4 && cidr == "":
				return fmt.Errorf("--cidr is required for an IPv4 subnet")
			case ipVersion == nemau.IPv6 && (cidr != "" || cmd.Flags().Changed("gateway")):
				return fmt.Errorf("--cidr and --gateway do not apply to an IPv6 subnet: the platform assigns the /64 and its gateway")
			}
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			req := nemau.CreateSubnetRequest{Name: args[1], CIDR: cidr, DNSServers: dnsServers}
			if ipVersion == nemau.IPv6 {
				req.IPVersion = nemau.IPv6
			}
			// A pointer tells an explicitly empty gateway (a subnet without one)
			// apart from an unset one, which lets the platform choose.
			if cmd.Flags().Changed("gateway") {
				req.GatewayIP = &gateway
			}
			ref, err := c.Network.CreateSubnet(ctx, args[0], req)
			if err != nil {
				return subnetOverlapHint(err)
			}
			return finish(cmd, ctx, ref, "subnet_id")
		},
	}
	create.Flags().StringVar(&cidr, "cidr", "", "CIDR range, for example 10.0.1.0/24 (required for IPv4)")
	create.Flags().StringVar(&gateway, "gateway", "", "gateway address (IPv4 only)")
	create.Flags().StringSliceVar(&dnsServers, "dns", nil, "DNS server (repeatable)")
	create.Flags().IntVar(&ipVersion, "ip-version", nemau.IPv4, "4, or 6 for a /64 assigned by the platform")

	cmd.AddCommand(updateCommand("update <id>", "Change a subnet's name",
		[]string{flagName}, "nemau network subnet update <id>", 1,
		func(c *nemau.Client, ctx context.Context, _ *updateFlags, u nemau.Update, args []string) (*nemau.TaskRef, error) {
			return c.Network.UpdateSubnet(ctx, args[0], u)
		}))

	cmd.AddCommand(create, &cobra.Command{
		Use: "delete <id>", Aliases: []string{"rm"}, Short: "Delete a subnet",
		Args: requireArgs(1, "nemau network subnet delete <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Network.DeleteSubnet(ctx, args[0])
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "subnet_id")
		},
	})
	return cmd
}

func interfaceCommand() *cobra.Command {
	return &cobra.Command{
		Use: "interface", Aliases: []string{"interfaces", "if"},
		Short: "List the instances' network interfaces",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.Network.ListNetworkInterfaces(ctx)
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "SLOT", "INSTANCE", "NETWORK", "MAC", "ADDRESSES"}, data: items}
			for _, it := range items {
				addrs := make([]string, 0, len(it.Addresses))
				for _, a := range it.Addresses {
					addrs = append(addrs, a.Address)
				}
				v.rows = append(v.rows, []string{it.ID, dash(it.Slot), dash(it.InstanceName),
					it.NetworkID, dash(it.MACAddress), joinLimit(addrs, 2)})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	}
}

func floatingIPCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "floating-ip", Aliases: []string{"fip"}, Short: "Manage floating IPs"}

	cmd.AddCommand(&cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List the floating IPs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.Network.ListFloatingIPs(ctx)
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "ADDRESS", "IP", "STATUS", "TARGET", "TARGET ID"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{it.ID, it.Address, ipVersionLabel(it.IPVersion), dash(it.Status),
					dash(it.TargetType), dash(it.TargetID)})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	})

	var externalNetwork, description string
	var fipVersion int
	alloc := &cobra.Command{
		Use: "allocate", Short: "Allocate a floating IP",
		Long: "Allocate a floating IP. An IPv6 one (--ip-version 6) is translated to the\n" +
			"target's private IPv4 on the exit node: the guest needs no IPv6 setup and\n" +
			"sees IPv6 clients as addresses of the translation range. A target holds at\n" +
			"most one IPv6 floating IP.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkIPVersion(fipVersion); err != nil {
				return err
			}
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			req := nemau.AllocateFloatingIPRequest{FloatingNetworkID: externalNetwork, Description: description}
			if fipVersion == nemau.IPv6 {
				req.IPVersion = nemau.IPv6
			}
			ref, err := c.Network.AllocateFloatingIPWith(ctx, req)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "floating_ip_id")
		},
	}
	alloc.Flags().StringVar(&externalNetwork, "external-network", "", "external network (default: the region's)")
	alloc.Flags().StringVar(&description, "description", "", "free-form description")
	alloc.Flags().IntVar(&fipVersion, "ip-version", nemau.IPv4, "4 or 6")

	var targetType string
	associate := &cobra.Command{
		Use: "associate <id> <target-id>", Short: "Associate a floating IP with a target",
		Args: requireArgs(2, "nemau network floating-ip associate <id> <target-id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if targetType != nemau.TargetInstance && targetType != nemau.TargetLoadBalancer {
				return fmt.Errorf("--target must be %q or %q", nemau.TargetInstance, nemau.TargetLoadBalancer)
			}
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Network.AssociateFloatingIP(ctx, args[0], targetType, args[1])
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "floating_ip_id")
		},
	}
	associate.Flags().StringVar(&targetType, "target", nemau.TargetInstance, "target type: instance or load_balancer")

	cmd.AddCommand(updateCommand("update <id>", "Change a floating IP's description",
		[]string{flagDescription}, "nemau network floating-ip update <id>", 1,
		func(c *nemau.Client, ctx context.Context, _ *updateFlags, u nemau.Update, args []string) (*nemau.TaskRef, error) {
			return c.Network.UpdateFloatingIP(ctx, args[0], u)
		}))

	cmd.AddCommand(alloc, associate,
		&cobra.Command{
			Use: "disassociate <id>", Short: "Detach a floating IP from its target",
			Args: requireArgs(1, "nemau network floating-ip disassociate <id>"),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, ctx, cancel, err := runCtx(cmd)
				if err != nil {
					return err
				}
				defer cancel()
				ref, err := c.Network.DisassociateFloatingIP(ctx, args[0])
				if err != nil {
					return err
				}
				return finish(cmd, ctx, ref, "floating_ip_id")
			},
		},
		&cobra.Command{
			Use: "release <id>", Aliases: []string{"rm"}, Short: "Release a floating IP",
			Args: requireArgs(1, "nemau network floating-ip release <id>"),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, ctx, cancel, err := runCtx(cmd)
				if err != nil {
					return err
				}
				defer cancel()
				ref, err := c.Network.ReleaseFloatingIP(ctx, args[0])
				if err != nil {
					return err
				}
				return finish(cmd, ctx, ref, "floating_ip_id")
			},
		})
	return cmd
}

func securityGroupCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "security-group", Aliases: []string{"sg"}, Short: "Manage security groups"}

	cmd.AddCommand(&cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List the security groups",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.Network.ListSecurityGroups(ctx)
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "NAME", "DESCRIPTION", "RULES", "EGRESS"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{it.ID, it.Name, dash(it.Description), itoa(len(it.Rules)), egressMode(it)})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use: "get <id>", Short: "Show one security group",
		Args: requireArgs(1, "nemau network security-group get <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			sg, err := c.Network.GetSecurityGroup(ctx, args[0])
			if err != nil {
				return err
			}
			ingress, egress := 0, 0
			for _, r := range sg.Rules {
				if r.Direction == "egress" {
					egress++
				} else {
					ingress++
				}
			}
			rows := [][]string{
				{"id", sg.ID},
				{"name", sg.Name},
				{"description", dash(sg.Description)},
				{"egress", egressMode(*sg)},
				{"ingress rules", itoa(ingress)},
				{"egress rules", itoa(egress)},
			}
			if !sg.EgressFiltering {
				rows = append(rows, []string{"note", "created before egress filtering: all outbound traffic is allowed " +
					"and egress rules have no effect (see enable-egress-filtering)"})
			}
			return render(cmd, view{columns: []string{"FIELD", "VALUE"}, rows: rows, data: sg, id: sg.ID})
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use: "rules <id>", Short: "List a group's rules",
		Args: requireArgs(1, "nemau network security-group rules <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			sg, err := c.Network.GetSecurityGroup(ctx, args[0])
			if err != nil {
				return err
			}
			if !sg.EgressFiltering {
				fmt.Fprintln(cmd.ErrOrStderr(), "this group predates egress filtering: its egress rules have no effect "+
					"(nemau network security-group enable-egress-filtering "+sg.ID+")")
			}
			// The column is REMOTE rather than SOURCE because an egress rule's
			// prefix is a destination.
			v := view{columns: []string{"ID", "DIRECTION", "ACTION", "PROTO", "PORTS", "REMOTE"}, data: sg.Rules}
			for _, r := range sg.Rules {
				ports := "-"
				if r.PortRangeMin != 0 || r.PortRangeMax != 0 {
					ports = fmt.Sprintf("%d-%d", r.PortRangeMin, r.PortRangeMax)
				}
				v.rows = append(v.rows, []string{r.ID, r.Direction, dash(r.Action),
					dash(r.Protocol), ports, dash(r.RemoteIPPrefix)})
				v.ids = append(v.ids, r.ID)
			}
			return render(cmd, v)
		},
	})

	var description string
	create := &cobra.Command{
		Use: "create <name>", Short: "Create a security group",
		Args: requireArgs(1, "nemau network security-group create <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Network.CreateSecurityGroup(ctx, args[0], description)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "security_group_id")
		},
	}
	create.Flags().StringVar(&description, "description", "", "free-form description")

	var rule nemau.SecurityGroupRuleRequest
	addRule := &cobra.Command{
		Use: "add-rule <group-id>", Short: "Add a rule to a group",
		Args: requireArgs(1, "nemau network security-group add-rule <group-id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Network.CreateSecurityGroupRule(ctx, args[0], rule)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "rule_id")
		},
	}
	rf := addRule.Flags()
	rf.StringVar(&rule.Direction, "direction", "ingress", "direction: ingress or egress")
	rf.StringVar(&rule.Protocol, "protocol", "", "protocol: tcp, udp, icmp")
	rf.IntVar(&rule.PortRangeMin, "port-min", 0, "first port")
	rf.IntVar(&rule.PortRangeMax, "port-max", 0, "last port")
	rf.StringVar(&rule.RemoteIPPrefix, "remote", "", "remote IP prefix: the source of ingress traffic, the destination of egress traffic, for example 0.0.0.0/0")

	enableEgress := &cobra.Command{
		Use:   "enable-egress-filtering <id>",
		Short: "Make a group created before egress filtering enforce its egress rules",
		Long: "Converts a security group created before egress filtering existed.\n\n" +
			"The group receives two egress rules that allow all outbound traffic (0.0.0.0/0 and ::/0), " +
			"then the instances carrying it have their outbound policy recomputed: an instance whose " +
			"groups all filter egress only sends what one of their egress rules allows. Traffic is " +
			"unchanged when the task completes; removing or narrowing the new rules afterwards takes effect.\n\n" +
			"The conversion cannot be undone. Running it again on a converted group does nothing.",
		Args: requireArgs(1, "nemau network security-group enable-egress-filtering <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Network.EnableSecurityGroupEgressFiltering(ctx, args[0])
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "security_group_id")
		},
	}

	cmd.AddCommand(updateCommand("update <id>", "Change a security group's name or description",
		[]string{flagName, flagDescription}, "nemau network security-group update <id>", 1,
		func(c *nemau.Client, ctx context.Context, _ *updateFlags, u nemau.Update, args []string) (*nemau.TaskRef, error) {
			return c.Network.UpdateSecurityGroup(ctx, args[0], u)
		}))

	cmd.AddCommand(create, addRule, enableEgress,
		&cobra.Command{
			Use: "delete-rule <group-id> <rule-id>", Short: "Delete a rule",
			Args: requireArgs(2, "nemau network security-group delete-rule <group-id> <rule-id>"),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, ctx, cancel, err := runCtx(cmd)
				if err != nil {
					return err
				}
				defer cancel()
				ref, err := c.Network.DeleteSecurityGroupRule(ctx, args[0], args[1])
				if err != nil {
					return err
				}
				return finish(cmd, ctx, ref, "rule_id")
			},
		},
		&cobra.Command{
			Use: "delete <id>", Aliases: []string{"rm"}, Short: "Delete a security group",
			Args: requireArgs(1, "nemau network security-group delete <id>"),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, ctx, cancel, err := runCtx(cmd)
				if err != nil {
					return err
				}
				defer cancel()
				ref, err := c.Network.DeleteSecurityGroup(ctx, args[0])
				if err != nil {
					return err
				}
				return finish(cmd, ctx, ref, "security_group_id")
			},
		})
	return cmd
}

// egressMode says whether a group's egress rules are enforced. "not filtered"
// is a group created before egress filtering: everything goes out.
func egressMode(sg nemau.SecurityGroup) string {
	if sg.EgressFiltering {
		return "filtered"
	}
	return "not filtered"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
