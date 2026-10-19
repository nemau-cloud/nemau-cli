package cli

import (
	"context"
	"fmt"
	"strings"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
	"github.com/spf13/cobra"
)

func newLoadBalancerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "load-balancer",
		Aliases: []string{"lb"},
		Short:   "Load balancers, listeners, TLS certificates, pools and health monitors",
	}
	cmd.AddCommand(lbCRUDCommand(), lbFlavorsCommand(), listenerCommand(), certificateCommand(), poolCommand())
	return cmd
}

// lbFlavorsCommand lists the tiers a load balancer can be created with.
func lbFlavorsCommand() *cobra.Command {
	return &cobra.Command{
		Use: "flavors", Aliases: []string{"flavor", "tiers"}, Short: "List the load-balancer tiers and their prices",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.LoadBalancer.ListFlavors(ctx)
			if err != nil {
				return err
			}
			v := view{columns: []string{"NAME", "APPLIANCES", "VCPU", "RAM", "HA", "MONTHLY"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{it.Name, itoa(it.Appliances), itoa(it.VCPUs),
					fmt.Sprintf("%d MB", it.RAMMB), yesNo(it.HighAvailability), monthlyPrice(it.MonthlyPrice, it.Currency)})
				v.ids = append(v.ids, it.Name)
			}
			return render(cmd, v)
		},
	}
}

// monthlyPrice renders a tier price, excluding VAT, the way the price list does.
func monthlyPrice(amount float64, currency string) string {
	return fmt.Sprintf("%.2f %s/month", amount, strings.ToUpper(currency))
}

// haSummary is the one-word HA state of a load balancer: "-" on a single
// appliance, otherwise the pair's status and, when degraded, why.
func haSummary(lb nemau.LoadBalancer) string {
	ha := lb.HighAvailability
	if ha == nil {
		return "-"
	}
	if ha.Reason != "" {
		return ha.Status + " (" + ha.Reason + ")"
	}
	return ha.Status
}

func lbCRUDCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "instance", Short: "Manage load balancers"}

	cmd.AddCommand(&cobra.Command{
		Use: "list", Short: "List the load balancers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.LoadBalancer.List(ctx)
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "NAME", "FLAVOR", "VIP", "PROVISIONING", "STATE", "HA", "LISTENERS", "POOLS"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{it.ID, it.Name, dash(it.Flavor), dash(it.VipAddress),
					dash(it.ProvisioningStatus), dash(it.OperatingStatus), haSummary(it),
					itoa(len(it.Listeners)), itoa(len(it.Pools))})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use: "get <id>", Short: "Show one load balancer",
		Args: requireArgs(1, "nemau lb instance get <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			lb, err := c.LoadBalancer.Get(ctx, args[0])
			if err != nil {
				return err
			}
			rows := [][]string{
				{"id", lb.ID},
				{"name", lb.Name},
				{"description", dash(lb.Description)},
				{"vip", dash(lb.VipAddress)},
				{"vip subnet", dash(lb.VipSubnetID)},
				{"engine", dash(lb.Provider)},
				{"flavor", dash(lb.Flavor)},
				{"provisioning", dash(lb.ProvisioningStatus)},
				{"state", dash(lb.OperatingStatus)},
				{"created", ago(lb.CreatedAt)},
			}
			if ha := lb.HighAvailability; ha != nil {
				rows = append(rows, []string{"high availability", haSummary(*lb)})
				for _, a := range ha.Appliances {
					rows = append(rows, []string{fmt.Sprintf("appliance %d", a.Index),
						fmt.Sprintf("%s, %s", a.State, dash(a.Address))})
				}
			}
			for _, l := range lb.Listeners {
				rows = append(rows, []string{"listener " + l.Name, fmt.Sprintf("%s/%d → %s", l.Protocol, l.ProtocolPort, dash(l.DefaultPoolID))})
			}
			for _, p := range lb.Pools {
				rows = append(rows, []string{"pool " + p.Name, fmt.Sprintf("%s, %s, %d member(s)", p.Protocol, p.LBAlgorithm, len(p.Members))})
			}
			return render(cmd, view{columns: []string{"FIELD", "VALUE"}, rows: rows, data: lb, id: lb.ID})
		},
	})

	var req nemau.CreateLoadBalancerRequest
	create := &cobra.Command{
		Use: "create <name>", Short: "Create a load balancer",
		Long: `Create a load balancer.

--flavor picks the tier (see "nemau lb flavors"): lb-small is one appliance and
the default; lb-ha, lb-medium and lb-large run two appliances on two hosts,
active and standby behind the same VIP. The tier cannot be changed afterwards.

Every new load balancer runs in automatic mode and accepts HTTP, HTTPS, TCP and
UDP listeners side by side.

Members receive traffic from the appliances' own addresses. On a
high-availability tier their security groups must admit both appliances
("nemau lb instance get" lists their addresses).`,
		Args: requireArgs(1, "nemau lb instance create <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			req.Name = args[0]
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.LoadBalancer.Create(ctx, req)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "load_balancer_id")
		},
	}
	create.Flags().StringVar(&req.VipSubnetID, "vip-subnet", "", "subnet to allocate the VIP in (required)")
	create.Flags().StringVar(&req.Description, "description", "", "free-form description")
	create.Flags().StringVar(&req.Flavor, "flavor", "", "tier: lb-small (default), lb-ha, lb-medium, lb-large")
	_ = create.MarkFlagRequired("vip-subnet")

	cmd.AddCommand(updateCommand("update <id>", "Change a load balancer's name or description",
		[]string{flagName, flagDescription}, "nemau lb instance update <id>", 1,
		func(c *nemau.Client, ctx context.Context, _ *updateFlags, u nemau.Update, args []string) (*nemau.TaskRef, error) {
			return c.LoadBalancer.UpdateLoadBalancer(ctx, args[0], u)
		}))

	cmd.AddCommand(create, &cobra.Command{
		Use: "delete <id>", Aliases: []string{"rm"}, Short: "Delete a load balancer",
		Args: requireArgs(1, "nemau lb instance delete <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.LoadBalancer.Delete(ctx, args[0])
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "load_balancer_id")
		},
	})
	return cmd
}

func listenerCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "listener", Aliases: []string{"listeners"}, Short: "Manage listeners"}

	cmd.AddCommand(&cobra.Command{
		Use: "list <lb-id>", Aliases: []string{"ls"}, Short: "List a load balancer's listeners",
		Args: requireArgs(1, "nemau lb listener list <lb-id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.LoadBalancer.ListListeners(ctx, args[0])
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "NAME", "PROTO", "PORT", "POOL", "STATE"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{it.ID, it.Name, it.Protocol, itoa(it.ProtocolPort),
					dash(it.DefaultPoolID), dash(it.OperatingStatus)})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	})

	var req nemau.CreateListenerRequest
	create := &cobra.Command{
		Use: "create <lb-id> <name>", Short: "Create a listener",
		Args: requireArgs(2, "nemau lb listener create <lb-id> <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			req.Name = args[1]
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.LoadBalancer.CreateListener(ctx, args[0], req)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "listener_id")
		},
	}
	create.Flags().StringVar(&req.Protocol, "protocol", "", "protocol: HTTP, HTTPS, TERMINATED_HTTPS, TCP, UDP (required)")
	create.Flags().IntVar(&req.ProtocolPort, "port", 0, "listening port (required)")
	create.Flags().StringVar(&req.DefaultPoolID, "default-pool", "", "default pool")
	create.Flags().IntVar(&req.ConnectionLimit, "connection-limit", 0, "connection limit")
	create.Flags().StringVar(&req.TLSCertificateRef, "tls-certificate", "",
		"ID of the certificate a TERMINATED_HTTPS listener serves (see \"nemau lb certificate\")")
	_ = create.MarkFlagRequired("protocol")
	_ = create.MarkFlagRequired("port")

	cmd.AddCommand(updateCommand("update <id>", "Change a listener's name, description, connection limit or certificate",
		[]string{flagName, flagDescription, flagConnectionLimit, flagTLSCertificate}, "nemau lb listener update <id>", 1,
		func(c *nemau.Client, ctx context.Context, _ *updateFlags, u nemau.Update, args []string) (*nemau.TaskRef, error) {
			return c.LoadBalancer.UpdateListener(ctx, args[0], u)
		}))

	cmd.AddCommand(create, &cobra.Command{
		Use: "delete <id>", Aliases: []string{"rm"}, Short: "Delete a listener",
		Args: requireArgs(1, "nemau lb listener delete <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.LoadBalancer.DeleteListener(ctx, args[0])
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "listener_id")
		},
	})
	return cmd
}

func poolCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "pool", Aliases: []string{"pools"}, Short: "Manage pools, members and health monitors"}

	cmd.AddCommand(&cobra.Command{
		Use: "list <lb-id>", Aliases: []string{"ls"}, Short: "List a load balancer's pools",
		Args: requireArgs(1, "nemau lb pool list <lb-id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.LoadBalancer.ListPools(ctx, args[0])
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "NAME", "PROTO", "ALGORITHM", "MEMBERS", "MONITORS"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{it.ID, it.Name, it.Protocol, it.LBAlgorithm,
					itoa(len(it.Members)), itoa(len(it.HealthMonitors))})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	})

	var req nemau.CreatePoolRequest
	create := &cobra.Command{
		Use: "create <lb-id> <name>", Short: "Create a pool",
		Args: requireArgs(2, "nemau lb pool create <lb-id> <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			req.Name = args[1]
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.LoadBalancer.CreatePool(ctx, args[0], req)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "pool_id")
		},
	}
	create.Flags().StringVar(&req.Protocol, "protocol", "", "protocol: HTTP, HTTPS, TERMINATED_HTTPS, TCP, UDP (required)")
	create.Flags().StringVar(&req.LBAlgorithm, "algorithm", "", "algorithm: ROUND_ROBIN, LEAST_CONNECTIONS, SOURCE_IP (required)")
	create.Flags().StringVar(&req.ListenerID, "listener", "", "listener to attach to")
	_ = create.MarkFlagRequired("protocol")
	_ = create.MarkFlagRequired("algorithm")

	memberList := &cobra.Command{
		Use: "members <pool-id>", Short: "List a pool's members",
		Args: requireArgs(1, "nemau lb pool members <pool-id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.LoadBalancer.ListPoolMembers(ctx, args[0])
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "NAME", "ADDRESS", "PORT", "WEIGHT", "STATE"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{it.ID, it.Name, it.Address, itoa(it.ProtocolPort),
					itoa(it.Weight), dash(it.OperatingStatus)})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	}

	var member nemau.AddPoolMemberRequest
	addMember := &cobra.Command{
		Use: "add-member <pool-id> <name>", Short: "Add a member to a pool",
		Args: requireArgs(2, "nemau lb pool add-member <pool-id> <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			member.Name = args[1]
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.LoadBalancer.AddPoolMember(ctx, args[0], member)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "member_id")
		},
	}
	addMember.Flags().StringVar(&member.Address, "address", "", "backend address (required)")
	addMember.Flags().IntVar(&member.ProtocolPort, "port", 0, "backend port (required)")
	addMember.Flags().IntVar(&member.Weight, "weight", 0, "weight in the balancing (default: 1)")
	addMember.Flags().StringVar(&member.SubnetID, "subnet", "", "backend subnet")
	_ = addMember.MarkFlagRequired("address")
	_ = addMember.MarkFlagRequired("port")

	var monitor nemau.CreateHealthMonitorRequest
	addMonitor := &cobra.Command{
		Use: "add-monitor <pool-id>", Short: "Add a health monitor to a pool",
		Long: `Add a health monitor to a pool.

UDP pools accept only UDP_CONNECT: failures on a UDP pool are detected
passively, and the API does not offer an active probe it would not perform.`,
		Args: requireArgs(1, "nemau lb pool add-monitor <pool-id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.LoadBalancer.CreateHealthMonitor(ctx, args[0], monitor)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "health_monitor_id")
		},
	}
	mf := addMonitor.Flags()
	mf.StringVar(&monitor.Type, "type", "", "type: HTTP, HTTPS, TCP, UDP_CONNECT (required)")
	mf.IntVar(&monitor.Delay, "delay", 0, "interval between two probes, in seconds (required)")
	mf.IntVar(&monitor.Timeout, "probe-timeout", 0, "timeout for one probe, in seconds (required)")
	mf.IntVar(&monitor.MaxRetries, "max-retries", 0, "consecutive failures before taking the backend out (required)")
	mf.StringVar(&monitor.HTTPMethod, "http-method", "", "HTTP method for the probe")
	mf.StringVar(&monitor.URLPath, "url-path", "", "path to probe")
	mf.StringVar(&monitor.ExpectedCodes, "expected-codes", "", "expected HTTP codes, for example 200-299")
	for _, name := range []string{"type", "delay", "probe-timeout", "max-retries"} {
		_ = addMonitor.MarkFlagRequired(name)
	}

	cmd.AddCommand(
		updateCommand("update <pool-id>", "Change a pool's name, description or algorithm",
			[]string{flagName, flagDescription, flagLBAlgorithm}, "nemau lb pool update <pool-id>", 1,
			func(c *nemau.Client, ctx context.Context, _ *updateFlags, u nemau.Update, args []string) (*nemau.TaskRef, error) {
				return c.LoadBalancer.UpdatePool(ctx, args[0], u)
			}),
		updateCommand("update-member <pool-id> <member-id>", "Change a member's name or weight",
			[]string{flagName, flagWeight}, "nemau lb pool update-member <pool-id> <member-id>", 2,
			func(c *nemau.Client, ctx context.Context, _ *updateFlags, u nemau.Update, args []string) (*nemau.TaskRef, error) {
				return c.LoadBalancer.UpdatePoolMember(ctx, args[0], args[1], u)
			}),
		updateCommand("update-monitor <pool-id> <monitor-id>", "Change a health monitor's probe settings",
			[]string{flagDelay, flagProbeTimeout, flagMaxRetries, flagExpectedCodes},
			"nemau lb pool update-monitor <pool-id> <monitor-id>", 2,
			func(c *nemau.Client, ctx context.Context, _ *updateFlags, u nemau.Update, args []string) (*nemau.TaskRef, error) {
				return c.LoadBalancer.UpdateHealthMonitor(ctx, args[0], args[1], u)
			}),
	)

	cmd.AddCommand(create, memberList, addMember, addMonitor,
		&cobra.Command{
			Use: "remove-member <pool-id> <member-id>", Short: "Remove a member from a pool",
			Args: requireArgs(2, "nemau lb pool remove-member <pool-id> <member-id>"),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, ctx, cancel, err := runCtx(cmd)
				if err != nil {
					return err
				}
				defer cancel()
				ref, err := c.LoadBalancer.RemovePoolMember(ctx, args[0], args[1])
				if err != nil {
					return err
				}
				return finish(cmd, ctx, ref, "member_id")
			},
		},
		&cobra.Command{
			Use: "remove-monitor <pool-id> <monitor-id>", Short: "Remove a health monitor",
			Args: requireArgs(2, "nemau lb pool remove-monitor <pool-id> <monitor-id>"),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, ctx, cancel, err := runCtx(cmd)
				if err != nil {
					return err
				}
				defer cancel()
				ref, err := c.LoadBalancer.DeleteHealthMonitor(ctx, args[0], args[1])
				if err != nil {
					return err
				}
				return finish(cmd, ctx, ref, "health_monitor_id")
			},
		},
		&cobra.Command{
			Use: "delete <pool-id>", Aliases: []string{"rm"}, Short: "Delete a pool",
			Args: requireArgs(1, "nemau lb pool delete <pool-id>"),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, ctx, cancel, err := runCtx(cmd)
				if err != nil {
					return err
				}
				defer cancel()
				ref, err := c.LoadBalancer.DeletePool(ctx, args[0])
				if err != nil {
					return err
				}
				return finish(cmd, ctx, ref, "pool_id")
			},
		})
	return cmd
}
