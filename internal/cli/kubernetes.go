package cli

import (
	"fmt"
	"os"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
	"github.com/spf13/cobra"
)

func newKubernetesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "kubernetes",
		Aliases: []string{"k8s"},
		Short:   "Managed Kubernetes clusters",
		Long: `Managed Kubernetes clusters.

Clusters are addressed by name. The name is unique within the project.`,
	}
	cmd.AddCommand(
		clusterCommand(),
		nodePoolCommand(),
		&cobra.Command{
			Use: "versions", Short: "List the available Kubernetes versions",
			RunE: func(cmd *cobra.Command, _ []string) error {
				c, ctx, cancel, err := runCtx(cmd)
				if err != nil {
					return err
				}
				defer cancel()
				versions, err := c.Kubernetes.ListVersions(ctx)
				if err != nil {
					return err
				}
				v := view{columns: []string{"VERSION"}, data: versions, ids: versions}
				for _, ver := range versions {
					v.rows = append(v.rows, []string{ver})
				}
				return render(cmd, v)
			},
		},
		&cobra.Command{
			Use: "cluster-classes", Short: "List the deployable ClusterClasses",
			RunE: func(cmd *cobra.Command, _ []string) error {
				c, ctx, cancel, err := runCtx(cmd)
				if err != nil {
					return err
				}
				defer cancel()
				classes, err := c.Kubernetes.ListClusterClasses(ctx)
				if err != nil {
					return err
				}
				v := view{columns: []string{"NAME", "NAMESPACE"}, data: classes}
				for _, cc := range classes {
					v.rows = append(v.rows, []string{cc.Name, cc.Namespace})
					v.ids = append(v.ids, cc.Name)
				}
				return render(cmd, v)
			},
		},
	)
	return cmd
}

func clusterCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "cluster", Aliases: []string{"clusters"}, Short: "Manage clusters"}

	cmd.AddCommand(&cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List the clusters",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.Kubernetes.ListClusters(ctx)
			if err != nil {
				return err
			}
			// The listing is a summary: the platform's lowercase status, and
			// the version, phase and readiness as of the last reconcile. The
			// topology is only on `cluster get`.
			v := view{columns: []string{"NAME", "STATUS", "VERSION", "PHASE", "CTRL-PLANE", "POOLS", "AGE"}, data: items}
			for _, it := range items {
				ctrlPlane := "-"
				if it.Specs.ControlPlaneReplicas > 0 {
					ctrlPlane = itoa(it.Specs.ControlPlaneReplicas)
					if it.Specs.ControlPlaneReady {
						ctrlPlane += " ready"
					}
				}
				pools := "-"
				if it.Specs.NodePoolsCount > 0 {
					pools = itoa(it.Specs.NodePoolsCount)
				}
				v.rows = append(v.rows, []string{
					it.Name, dash(it.Status), dash(it.Specs.KubernetesVersion), dash(it.Specs.Phase),
					ctrlPlane, pools, ago(it.CreatedAt),
				})
				v.ids = append(v.ids, it.Name)
			}
			return render(cmd, v)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use: "get <name>", Short: "Show one cluster",
		Args: requireArgs(1, "nemau k8s cluster get <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			cl, err := c.Kubernetes.GetCluster(ctx, args[0])
			if err != nil {
				return err
			}
			endpoint := "-"
			if cl.Status.ControlPlaneEndpoint != nil {
				endpoint = fmt.Sprintf("%s:%d", cl.Status.ControlPlaneEndpoint.Host, cl.Status.ControlPlaneEndpoint.Port)
			}
			rows := [][]string{
				{"name", cl.Name},
				{"namespace", dash(cl.Namespace)},
				{"version", dash(cl.Spec.Topology.Version)},
				{"class", dash(cl.Spec.Topology.Class)},
				{"phase", dash(cl.Status.Phase)},
				{"infrastructure ready", yesNo(cl.Status.InfrastructureReady)},
				{"control plane ready", yesNo(cl.Status.ControlPlaneReady)},
				{"endpoint", endpoint},
				{"control plane replicas", i32toa(cl.Spec.Topology.ControlPlaneReplicas)},
				{"created", ago(cl.CreatedAt)},
			}
			for _, np := range cl.Spec.Topology.NodePools {
				rows = append(rows, []string{"pool " + np.Name,
					fmt.Sprintf("%d replicas, %d vCPU, %d MB, %d GB", np.Replicas, np.VCPUs, np.RAMMB, np.DiskGB)})
			}
			return render(cmd, view{columns: []string{"FIELD", "VALUE"}, rows: rows, data: cl, id: cl.Name})
		},
	})

	var req nemau.CreateClusterRequest
	var nodePools []string
	create := &cobra.Command{
		Use: "create <name>", Short: "Create a cluster",
		Long: `Create a cluster.

The control-plane size is either --control-plane-flavor or the three
--control-plane-vcpus, --control-plane-ram and --control-plane-disk values.

Without --node-pool, the cluster gets the console's default pool: "workers",
class "default-worker", 3 nodes of 2 vCPU, 4096 MB and 20 GB (or of the
--worker-flavor size when it is set). Each --node-pool declares one pool
instead; repeat the flag for several:

  --node-pool ` + nodePoolSyntax + `

In a --node-pool, class defaults to "default-worker", replicas to 3 (or to
autoscaler-min when the autoscaler is set), and the size to --worker-flavor,
else to 2 vCPU, 4096 MB, 20 GB. Add pools to a running cluster with
"nemau k8s node-pool add".

Provisioning takes several minutes; raise --timeout if needed.`,
		Example: `  nemau k8s cluster create prod --version v1.31.0 \
    --control-plane-vcpus 2 --control-plane-ram 4096 --control-plane-disk 20

  nemau k8s cluster create prod --version v1.31.0 --control-plane-flavor <flavor-id> \
    --node-pool name=workers,replicas=3,vcpus=4,ram=8192,disk=50 \
    --node-pool name=batch,autoscaler-min=1,autoscaler-max=10,flavor=<flavor-id>`,
		Args: requireArgs(1, "nemau k8s cluster create <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			req.Name = args[0]
			if req.ControlPlaneFlavorID == "" &&
				(req.ControlPlaneVCPUs == 0 || req.ControlPlaneRAMMB == 0 || req.ControlPlaneDiskGB == 0) {
				return fmt.Errorf("need --control-plane-flavor, or all three of --control-plane-vcpus, --control-plane-ram and --control-plane-disk")
			}
			if req.ControlPlaneReplicas < 1 || req.ControlPlaneReplicas > maxControlPlaneReplicas {
				return fmt.Errorf("--control-plane-replicas must be between 1 and %d", maxControlPlaneReplicas)
			}
			pools, err := buildNodePools(nodePools, req.WorkerFlavorID)
			if err != nil {
				return err
			}
			req.NodePools = pools
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Kubernetes.CreateCluster(ctx, req)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "cluster_id")
		},
	}
	cf := create.Flags()
	cf.StringVar(&req.KubernetesVersion, "version", "", "Kubernetes version (required)")
	cf.StringVar(&req.ClusterClass, "class", "", "ClusterClass to use")
	cf.StringVar(&req.ControlPlaneFlavorID, "control-plane-flavor", "", "control-plane flavor")
	cf.IntVar(&req.ControlPlaneVCPUs, "control-plane-vcpus", 0, "vCPUs per control-plane node")
	cf.IntVar(&req.ControlPlaneRAMMB, "control-plane-ram", 0, "memory in MB per control-plane node")
	cf.IntVar(&req.ControlPlaneDiskGB, "control-plane-disk", 0, "disk in GB per control-plane node")
	cf.IntVar(&req.ControlPlaneReplicas, "control-plane-replicas", defaultControlPlaneReplicas, "number of control-plane nodes, 1 to 9 (odd: etcd needs a quorum)")
	cf.StringVar(&req.WorkerFlavorID, "worker-flavor", "", "flavor of the node pools given no size")
	cf.StringArrayVar(&nodePools, "node-pool", nil, "a node pool, as "+nodePoolSyntax+" (repeatable; default: one \"workers\" pool)")
	_ = create.MarkFlagRequired("version")

	var replicas int
	scaleCP := &cobra.Command{
		Use: "scale-control-plane <name>", Short: "Change the number of control-plane nodes",
		Long: `Change the number of control-plane nodes.

Use an odd number: etcd needs a quorum.`,
		Args: requireArgs(1, "nemau k8s cluster scale-control-plane <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Kubernetes.ScaleControlPlane(ctx, args[0], replicas)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "cluster_id")
		},
	}
	scaleCP.Flags().IntVar(&replicas, "replicas", 0, "number of nodes (required)")
	_ = scaleCP.MarkFlagRequired("replicas")

	var version string
	upgrade := &cobra.Command{
		Use: "upgrade <name>", Short: "Upgrade the Kubernetes version",
		Long: `Upgrade the Kubernetes version.

A single task runs the preflight checks against the target version, opens a
maintenance window, then starts the upgrade. A preflight with blocking checks
fails the task. Nothing has to be opened beforehand.

The task only starts the upgrade. When it completes, the nodes are still being
replaced one by one. Follow the progress with "nemau k8s cluster get".

--version takes x.y.z, with or without a leading "v".

Requires the "kubernetes-guided-upgrades" organization feature.`,
		Args: requireArgs(1, "nemau k8s cluster upgrade <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Kubernetes.UpgradeCluster(ctx, args[0], version)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref, "cluster_id")
		},
	}
	upgrade.Flags().StringVar(&version, "version", "", "target Kubernetes version, such as 1.31.2 (required)")
	_ = upgrade.MarkFlagRequired("version")

	var kubeconfigOut string
	kubeconfig := &cobra.Command{
		Use: "kubeconfig <name>", Short: "Fetch a cluster's kubeconfig",
		Long: `Fetch a cluster's kubeconfig.

The kubeconfig carries cluster-admin rights and reading it is audited
(GET_KUBECONFIG). Treat it as a secret.`,
		Args: requireArgs(1, "nemau k8s cluster kubeconfig <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			kc, err := c.Kubernetes.GetKubeconfig(ctx, args[0])
			if err != nil {
				return err
			}
			if kubeconfigOut != "" {
				if err := os.WriteFile(kubeconfigOut, []byte(kc), 0o600); err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "kubeconfig written to %s (mode 0600)\n", kubeconfigOut)
				return nil
			}
			fmt.Fprint(cmd.OutOrStdout(), kc)
			return nil
		},
	}
	kubeconfig.Flags().StringVar(&kubeconfigOut, "out", "", "write to this file instead of standard output")

	cmd.AddCommand(create, scaleCP, upgrade, kubeconfig,
		&cobra.Command{
			Use: "delete <name>", Aliases: []string{"rm"}, Short: "Delete a cluster",
			Args: requireArgs(1, "nemau k8s cluster delete <name>"),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, ctx, cancel, err := runCtx(cmd)
				if err != nil {
					return err
				}
				defer cancel()
				ref, err := c.Kubernetes.DeleteCluster(ctx, args[0])
				if err != nil {
					return err
				}
				return finish(cmd, ctx, ref, "cluster_id")
			},
		})
	return cmd
}

func nodePoolCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "node-pool", Aliases: []string{"nodepool", "np"}, Short: "Manage node pools"}

	cmd.AddCommand(&cobra.Command{
		Use: "list <cluster>", Aliases: []string{"ls"}, Short: "List a cluster's node pools",
		Args: requireArgs(1, "nemau k8s node-pool list <cluster>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			cl, err := c.Kubernetes.GetCluster(ctx, args[0])
			if err != nil {
				return err
			}
			pools := cl.Spec.Topology.NodePools
			v := view{columns: []string{"NAME", "CLASS", "REPLICAS", "VCPU", "RAM", "DISK", "AUTOSCALER"}, data: pools}
			for _, np := range pools {
				autoscaler := "-"
				if np.AutoscalerMin != 0 || np.AutoscalerMax != 0 {
					autoscaler = fmt.Sprintf("%d-%d", np.AutoscalerMin, np.AutoscalerMax)
				}
				v.rows = append(v.rows, []string{np.Name, dash(np.Class), i32toa(np.Replicas),
					itoa(np.VCPUs), itoa(np.RAMMB), itoa(np.DiskGB), autoscaler})
				v.ids = append(v.ids, np.Name)
			}
			return render(cmd, v)
		},
	})

	var add nemau.AddNodePoolRequest
	addCmd := &cobra.Command{
		Use: "add <cluster> <name>", Short: "Add a node pool",
		Long: `Add a node pool to a running cluster.

The node size is either --flavor (an organization flavor) or all three of
--vcpus, --ram and --disk. --class is the node class the ClusterClass defines;
the console's default pool uses "default-worker".`,
		Example: `  nemau k8s node-pool add prod batch --class default-worker \
    --vcpus 4 --ram 8192 --disk 50 --replicas 2

  nemau k8s node-pool add prod gpu --class default-worker --flavor <flavor-id> \
    --autoscaler-min 1 --autoscaler-max 5 --replicas 1`,
		Args: requireArgs(2, "nemau k8s node-pool add <cluster> <name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			add.PoolName = args[1]
			if add.FlavorID == "" && (add.VCPUs == 0 || add.RAMMB == 0 || add.DiskGB == 0) {
				return fmt.Errorf("need --flavor, or all three of --vcpus, --ram and --disk")
			}
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Kubernetes.AddNodePool(ctx, args[0], add)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref)
		},
	}
	af := addCmd.Flags()
	af.StringVar(&add.Class, "class", "", "node class defined by the ClusterClass (required)")
	af.StringVar(&add.FlavorID, "flavor", "", "node flavor")
	af.IntVar(&add.VCPUs, "vcpus", 0, "vCPUs per node")
	af.IntVar(&add.RAMMB, "ram", 0, "memory in MB per node")
	af.IntVar(&add.DiskGB, "disk", 0, "disk in GB per node")
	af.IntVar(&add.Replicas, "replicas", 0, "number of nodes, 0 to 100")
	af.IntVar(&add.AutoscalerMin, "autoscaler-min", 0, "autoscaler lower bound")
	af.IntVar(&add.AutoscalerMax, "autoscaler-max", 0, "autoscaler upper bound")
	_ = addCmd.MarkFlagRequired("class")

	var replicas int
	scale := &cobra.Command{
		Use: "scale <cluster> <pool>", Short: "Change a pool's node count",
		Args: requireArgs(2, "nemau k8s node-pool scale <cluster> <pool>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Kubernetes.ScaleNodePool(ctx, args[0], args[1], replicas)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref)
		},
	}
	scale.Flags().IntVar(&replicas, "replicas", 0, "number of nodes (required)")
	_ = scale.MarkFlagRequired("replicas")

	var min, max int
	autoscaler := &cobra.Command{
		Use: "autoscaler <cluster> <pool>", Short: "Configure a pool's autoscaler",
		Args: requireArgs(2, "nemau k8s node-pool autoscaler <cluster> <pool>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := c.Kubernetes.SetAutoscaler(ctx, args[0], args[1], min, max)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref)
		},
	}
	autoscaler.Flags().IntVar(&min, "min", 0, "lower bound (required)")
	autoscaler.Flags().IntVar(&max, "max", 0, "upper bound (required)")
	_ = autoscaler.MarkFlagRequired("min")
	_ = autoscaler.MarkFlagRequired("max")

	cmd.AddCommand(addCmd, scale, autoscaler,
		&cobra.Command{
			Use: "remove <cluster> <pool>", Aliases: []string{"rm"}, Short: "Remove a node pool",
			Args: requireArgs(2, "nemau k8s node-pool remove <cluster> <pool>"),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, ctx, cancel, err := runCtx(cmd)
				if err != nil {
					return err
				}
				defer cancel()
				ref, err := c.Kubernetes.RemoveNodePool(ctx, args[0], args[1])
				if err != nil {
					return err
				}
				return finish(cmd, ctx, ref)
			},
		})
	return cmd
}
