package cli

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
)

// The console's defaults for a new cluster: three control-plane nodes and one
// pool of three 2 vCPU / 4 GiB / 20 GB workers. `cluster create` applies the
// same ones, so a cluster made from the terminal matches one made in the
// console unless a flag says otherwise.
const (
	defaultControlPlaneReplicas = 3
	defaultPoolName             = "workers"
	defaultPoolClass            = "default-worker"
	defaultPoolReplicas         = 3
	defaultPoolVCPUs            = 2
	defaultPoolRAMMB            = 4096
	defaultPoolDiskGB           = 20
)

// The API's bounds, checked here only so a typo fails before the request: the
// API checks them again and is the one that decides.
const (
	maxControlPlaneReplicas = 9
	maxPoolReplicas         = 100
)

// A pool name becomes a Cluster API MachineDeployment name: a DNS label.
var poolNameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

const nodePoolSyntax = "name=<name>[,class=<class>][,replicas=<n>][,flavor=<id> | ,vcpus=<n>,ram=<MB>,disk=<GB>][,autoscaler-min=<n>,autoscaler-max=<n>]"

// buildNodePools turns the --node-pool values into the request's pools. With
// none, it returns the console's default pool. A pool given no size takes the
// cluster's --worker-flavor when there is one, the console's default size
// otherwise.
func buildNodePools(specs []string, workerFlavor string) ([]nemau.NodePoolInit, error) {
	if len(specs) == 0 {
		specs = []string{"name=" + defaultPoolName}
	}
	pools := make([]nemau.NodePoolInit, 0, len(specs))
	seen := make(map[string]bool, len(specs))
	for _, spec := range specs {
		pool, err := parseNodePool(spec, workerFlavor != "")
		if err != nil {
			return nil, fmt.Errorf("--node-pool %q: %w", spec, err)
		}
		if seen[pool.Name] {
			return nil, fmt.Errorf("--node-pool: pool name %q is used twice", pool.Name)
		}
		seen[pool.Name] = true
		pools = append(pools, pool)
	}
	return pools, nil
}

func parseNodePool(spec string, hasWorkerFlavor bool) (nemau.NodePoolInit, error) {
	pool := nemau.NodePoolInit{Class: defaultPoolClass}
	set := map[string]bool{}
	for _, field := range strings.Split(spec, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if !ok || value == "" {
			return pool, fmt.Errorf("expected key=value, got %q (syntax: %s)", field, nodePoolSyntax)
		}
		if set[key] {
			return pool, fmt.Errorf("%s is given twice", key)
		}
		set[key] = true
		var target *int
		switch key {
		case "name":
			pool.Name = value
			continue
		case "class":
			pool.Class = value
			continue
		case "flavor":
			pool.FlavorID = value
			continue
		case "replicas":
			target = &pool.Replicas
		case "vcpus":
			target = &pool.VCPUs
		case "ram":
			target = &pool.RAMMB
		case "disk":
			target = &pool.DiskGB
		case "autoscaler-min":
			target = &pool.AutoscalerMin
		case "autoscaler-max":
			target = &pool.AutoscalerMax
		default:
			return pool, fmt.Errorf("unknown key %q (syntax: %s)", key, nodePoolSyntax)
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return pool, fmt.Errorf("%s must be a non-negative integer, got %q", key, value)
		}
		*target = n
	}

	if !poolNameRE.MatchString(pool.Name) {
		return pool, fmt.Errorf("name must be a DNS label: lowercase letters, digits and hyphens, 63 characters at most")
	}

	autoscaled := set["autoscaler-min"] || set["autoscaler-max"]
	if autoscaled {
		if !set["autoscaler-min"] || !set["autoscaler-max"] {
			return pool, fmt.Errorf("autoscaler-min and autoscaler-max go together")
		}
		if pool.AutoscalerMin < 1 || pool.AutoscalerMax < pool.AutoscalerMin || pool.AutoscalerMax > maxPoolReplicas {
			return pool, fmt.Errorf("the autoscaler needs 1 <= autoscaler-min <= autoscaler-max <= %d", maxPoolReplicas)
		}
	}
	switch {
	case set["replicas"]:
	case autoscaled:
		// As in the console: an autoscaled pool starts at its floor.
		pool.Replicas = pool.AutoscalerMin
	default:
		pool.Replicas = defaultPoolReplicas
	}
	if pool.Replicas > maxPoolReplicas {
		return pool, fmt.Errorf("replicas must be between 0 and %d", maxPoolReplicas)
	}

	sized := set["vcpus"] || set["ram"] || set["disk"]
	switch {
	case pool.FlavorID != "" && sized:
		return pool, fmt.Errorf("give either flavor or vcpus/ram/disk, not both")
	case sized && (pool.VCPUs == 0 || pool.RAMMB == 0 || pool.DiskGB == 0):
		return pool, fmt.Errorf("need all three of vcpus, ram and disk")
	case pool.FlavorID == "" && !sized && !hasWorkerFlavor:
		pool.VCPUs, pool.RAMMB, pool.DiskGB = defaultPoolVCPUs, defaultPoolRAMMB, defaultPoolDiskGB
	}
	return pool, nil
}
