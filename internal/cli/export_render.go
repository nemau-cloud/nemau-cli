package cli

import (
	"fmt"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
)

// renderInventory turns an inventory into HCL.
//
// Blocks are emitted in dependency order: a resource can only be referenced by
// address once it has been added, so networks come before subnets, security
// groups before instances, and so on. In any other order the references would
// fall back to literal ids.
func renderInventory(found inventory) *hclDocument {
	document := newHCLDocument()

	renderNetworks(document, found)
	renderSecurityGroups(document, found)
	renderKeypairs(document, found)
	renderInstances(document, found)
	renderVolumes(document, found)
	renderFloatingIPs(document, found)
	renderContainers(document, found)
	renderDNS(document, found)
	renderLoadBalancers(document, found)
	renderClusters(document, found)

	return document
}

func renderNetworks(d *hclDocument, found inventory) {
	for _, network := range found.Networks {
		// The region's external network is not declared: it exists for every
		// project and creating it would fail.
		if network.External {
			continue
		}
		name := d.name("nemau_network", network.Name)
		d.add("nemau_network", name, network.ID, func(b *hclBlock) {
			b.setAlways("name", network.Name)
			b.set("description", network.Description)
		})
	}

	for _, subnet := range found.Subnets {
		if _, known := d.address[subnet.NetworkID]; !known {
			// Belongs to a network outside the export (the external one, or a
			// network the credential cannot see). Declaring it would produce a
			// subnet with a dangling parent.
			continue
		}
		name := d.name("nemau_subnet", subnet.Name)
		d.add("nemau_subnet", name, subnet.ID, func(b *hclBlock) {
			b.setAlways("network_id", d.ref(subnet.NetworkID))
			b.setAlways("name", subnet.Name)
			if subnet.IPVersion == nemau.IPv6 {
				// The platform assigns the /64 and its gateway; the provider
				// refuses both on an IPv6 subnet.
				b.setAlways("ip_version", nemau.IPv6)
				b.comment = "Current range: " + subnet.CIDR +
					"\nRecreating it assigns a different /64: every instance address changes."
			} else {
				b.setAlways("cidr", subnet.CIDR)
				b.set("gateway_ip", subnet.GatewayIP)
			}
			b.set("dns_servers", subnet.DNSServers)
		})
	}
}

func renderSecurityGroups(d *hclDocument, found inventory) {
	for _, group := range found.SecurityGroups {
		// A group that filters egress creates its two allow-all egress rules
		// itself. Exporting them would make an apply elsewhere fail, since the
		// new group already has them, so they are left out and a comment on the
		// group explains how to manage them.
		defaults := 0
		for _, rule := range group.Rules {
			if isDefaultEgressRule(group, rule) {
				defaults++
			}
		}
		groupName := d.name("nemau_security_group", group.Name)
		d.add("nemau_security_group", groupName, group.ID, func(b *hclBlock) {
			b.setAlways("name", group.Name)
			b.set("description", group.Description)
			if defaults > 0 {
				b.comment = "The group creates its default egress rules (0.0.0.0/0, ::/0): they are not exported." +
					"\nTo restrict egress, import them as <group id>:<rule id> and delete them;" +
					"\n`nemau network security-group rules <group id>` lists the rule ids."
			}
		})

		index := 0
		for _, rule := range group.Rules {
			if isDefaultEgressRule(group, rule) {
				continue
			}
			index++
			ruleName := d.name("nemau_security_group_rule",
				fmt.Sprintf("%s_%d", group.Name, index))
			// A rule imports as <group id>:<rule id>: the API serves it only
			// inside its group.
			d.add("nemau_security_group_rule", ruleName, group.ID+":"+rule.ID, func(b *hclBlock) {
				b.setAlways("security_group_id", d.ref(group.ID))
				b.setAlways("direction", rule.Direction)
				b.set("protocol", rule.Protocol)
				b.set("port_range_min", rule.PortRangeMin)
				b.set("port_range_max", rule.PortRangeMax)
				b.set("remote_ip_prefix", rule.RemoteIPPrefix)
			})
		}
	}
}

// isDefaultEgressRule is one of the two rules a filtering group is created
// with: egress to 0.0.0.0/0 or ::/0, any protocol, any port. On a group created
// before egress filtering such a rule was written by hand and is exported.
func isDefaultEgressRule(group nemau.SecurityGroup, rule nemau.SecurityGroupRule) bool {
	if !group.EgressFiltering || rule.Direction != "egress" || rule.Protocol != "" ||
		rule.PortRangeMin != 0 || rule.PortRangeMax != 0 {
		return false
	}
	return rule.RemoteIPPrefix == "0.0.0.0/0" || rule.RemoteIPPrefix == "::/0"
}

func renderKeypairs(d *hclDocument, found inventory) {
	for _, keypair := range found.Keypairs {
		name := d.name("nemau_keypair", keypair.Name)
		d.add("nemau_keypair", name, keypair.Name, func(b *hclBlock) {
			b.setAlways("name", keypair.Name)
			// Only the public key is exported: the private key of a
			// platform-generated pair was shown once and never stored. Applying
			// elsewhere needs the operator's own key.
			b.set("public_key", keypair.PublicKey)
			if keypair.PublicKey == "" {
				b.comment = "Public key not exposed by the API: set public_key before applying."
			}
		})
	}
}

func renderInstances(d *hclDocument, found inventory) {
	// An instance does not report the network it sits on; the interface listing
	// is the only place that binding appears.
	networkOf := map[string]string{}
	for _, iface := range found.Interfaces {
		if _, seen := networkOf[iface.InstanceID]; !seen {
			networkOf[iface.InstanceID] = iface.NetworkID
		}
	}

	for _, instance := range found.Instances {
		name := d.name("nemau_instance", instance.Name)
		d.add("nemau_instance", name, instance.ID, func(b *hclBlock) {
			b.setAlways("name", instance.Name)
			b.set("description", instance.Description)
			if instance.Image != nil {
				b.setAlways("image_id", instance.Image.ID)
			}
			if instance.Flavor != nil && instance.Flavor.ID != "" {
				b.setAlways("flavor_id", instance.Flavor.ID)
			} else {
				b.setAlways("vcpus", instance.Sizing.VCPUs)
				b.setAlways("ram_mb", instance.Sizing.RAMMB)
				b.setAlways("disk_gb", instance.Sizing.DiskGB)
			}
			if networkID := networkOf[instance.ID]; networkID != "" {
				b.setAlways("network_id", d.ref(networkID))
			}
			if len(instance.SecurityGroups) > 0 {
				refs := make([]string, 0, len(instance.SecurityGroups))
				for _, group := range instance.SecurityGroups {
					refs = append(refs, hclLiteral(d.ref(group.ID)))
				}
				b.setAlways("security_group_ids", hclRef("["+joinComma(refs)+"]"))
			}
			b.set("key_name", instance.KeyName)
			// Power state is part of the desired state, so a stopped instance is
			// recreated stopped.
			b.setAlways("started", instance.Status == "running")
		})
	}
}

func renderVolumes(d *hclDocument, found inventory) {
	for _, volume := range found.Volumes {
		name := d.name("nemau_volume", volume.Name)
		d.add("nemau_volume", name, volume.ID, func(b *hclBlock) {
			b.setAlways("name", volume.Name)
			b.set("description", volume.Description)
			b.setAlways("size", volume.Size)
		})

		for _, attachment := range volume.Attachments {
			attachName := d.name("nemau_volume_attachment", volume.Name)
			// The attachment's synthetic id is what the provider stores.
			id := volume.ID + ":" + attachment.ServerID
			d.add("nemau_volume_attachment", attachName, id, func(b *hclBlock) {
				b.setAlways("volume_id", d.ref(volume.ID))
				b.setAlways("instance_id", d.ref(attachment.ServerID))
			})
		}
	}
}

func renderFloatingIPs(d *hclDocument, found inventory) {
	for _, floating := range found.FloatingIPs {
		name := d.name("nemau_floating_ip", floating.Address)
		d.add("nemau_floating_ip", name, floating.ID, func(b *hclBlock) {
			if floating.IPVersion == nemau.IPv6 {
				b.setAlways("ip_version", nemau.IPv6)
			}
			b.set("description", floating.Description)
			b.comment = "Current address: " + floating.Address +
				"\nRecreating it allocates a different one: update your DNS zones."
		})

		if floating.TargetID == "" {
			continue
		}
		assocName := d.name("nemau_floating_ip_association", floating.Address)
		d.add("nemau_floating_ip_association", assocName, floating.ID, func(b *hclBlock) {
			b.setAlways("floating_ip_id", d.ref(floating.ID))
			b.setAlways("target_type", floating.TargetType)
			b.setAlways("target_id", d.ref(floating.TargetID))
		})
	}
}

func renderContainers(d *hclDocument, found inventory) {
	for _, container := range found.Containers {
		name := d.name("nemau_container", container.Name)
		d.add("nemau_container", name, container.Name, func(b *hclBlock) {
			b.setAlways("name", container.Name)
			if container.Count > 0 {
				b.comment = fmt.Sprintf(
					"Holds %d object(s), %d bytes; the contents are not exported here.\nUse \"nemau storage export %s <directory>\".",
					container.Count, container.Bytes, container.Name)
			}
		})
	}
}

// renderDNS emits each zone, the proof of its ownership, and its record sets.
//
// A zone recreated elsewhere starts pending, and the API refuses record sets on
// a pending zone. So for an active zone the record sets take their zone_id from
// a nemau_dns_zone_verification rather than from the zone, which makes
// Terraform wait for the proof before writing records. The verification block
// has no import: in the project it came from, the zone is already active and
// creating the verification only confirms it.
func renderDNS(d *hclDocument, found inventory) {
	for _, zone := range found.DNSZones {
		zoneName := d.name("nemau_dns_zone", zone.Name)
		pending := zone.Status == nemau.DNSZoneStatusPendingVerification
		d.add("nemau_dns_zone", zoneName, zone.ID, func(b *hclBlock) {
			b.setAlways("name", zone.Name)
			b.setAlways("email", zone.Email)
			b.set("description", zone.Description)
			b.set("ttl", zone.TTL)
			if pending {
				b.comment = "Pending verification: publish the TXT record in verification_record_name /\n" +
					"verification_record_value, then declare a nemau_dns_zone_verification."
			}
		})

		zoneID := d.ref(zone.ID)
		if !pending {
			verificationName := d.name("nemau_dns_zone_verification", zone.Name)
			d.add("nemau_dns_zone_verification", verificationName, "", func(b *hclBlock) {
				b.setAlways("zone_id", zoneID)
				b.comment = "Ownership proof. Recreated in another project, the zone waits for its\n" +
					"TXT record (verification_record_name / verification_record_value) to be published."
			})
			zoneID = hclRef("nemau_dns_zone_verification." + verificationName + ".zone_id")
		}

		for _, recordSet := range found.DNSRecordSets[zone.ID] {
			// SOA and NS are created and owned by the zone itself; declaring
			// them would collide with what the platform already manages.
			if recordSet.Protected {
				continue
			}
			recordName := d.name("nemau_dns_record_set", zone.Name+"_"+recordSet.Name+"_"+recordSet.Type)
			d.add("nemau_dns_record_set", recordName, recordSet.ID, func(b *hclBlock) {
				b.setAlways("zone_id", zoneID)
				b.setAlways("name", recordSet.Name)
				b.setAlways("type", recordSet.Type)
				b.setAlways("records", recordSet.Records)
				b.set("ttl", recordSet.TTL)
			})
		}
	}
}

func renderLoadBalancers(d *hclDocument, found inventory) {
	for _, balancer := range found.LoadBalancers {
		lbName := d.name("nemau_load_balancer", balancer.Name)
		d.add("nemau_load_balancer", lbName, balancer.ID, func(b *hclBlock) {
			b.setAlways("name", balancer.Name)
			b.set("description", balancer.Description)
			b.setAlways("vip_subnet_id", d.ref(balancer.VipSubnetID))
			// provider_engine is read-only in the provider, so it is not
			// exported. A recreated load balancer runs in automatic mode, which
			// serves every protocol the original's listeners use.
			b.set("flavor", balancer.Flavor)
			if balancer.VipAddress != "" {
				b.comment = "Current VIP: " + balancer.VipAddress
			}
		})

		// Pools before listeners: a listener may point at a pool by
		// default_pool_id, and the reference only resolves if the pool exists.
		for _, pool := range balancer.Pools {
			poolName := d.name("nemau_pool", balancer.Name+"_"+pool.Name)
			d.add("nemau_pool", poolName, pool.ID, func(b *hclBlock) {
				b.setAlways("load_balancer_id", d.ref(balancer.ID))
				b.setAlways("name", pool.Name)
				b.setAlways("protocol", pool.Protocol)
				b.setAlways("lb_algorithm", pool.LBAlgorithm)
			})

			for _, member := range pool.Members {
				memberName := d.name("nemau_pool_member", pool.Name+"_"+member.Name)
				d.add("nemau_pool_member", memberName, member.ID, func(b *hclBlock) {
					b.setAlways("pool_id", d.ref(pool.ID))
					b.setAlways("name", member.Name)
					b.setAlways("address", member.Address)
					b.setAlways("protocol_port", member.ProtocolPort)
					b.set("weight", member.Weight)
					b.set("subnet_id", member.SubnetID)
				})
			}

			for _, monitor := range pool.HealthMonitors {
				monitorName := d.name("nemau_health_monitor", pool.Name)
				d.add("nemau_health_monitor", monitorName, monitor.ID, func(b *hclBlock) {
					b.setAlways("pool_id", d.ref(pool.ID))
					b.setAlways("type", monitor.Type)
					b.setAlways("delay", monitor.Delay)
					b.setAlways("timeout", monitor.Timeout)
					b.setAlways("max_retries", monitor.MaxRetries)
					b.set("http_method", monitor.HTTPMethod)
					b.set("url_path", monitor.URLPath)
					b.set("expected_codes", monitor.ExpectedCodes)
				})
			}
		}

		for _, listener := range balancer.Listeners {
			listenerName := d.name("nemau_listener", balancer.Name+"_"+listener.Name)
			d.add("nemau_listener", listenerName, listener.ID, func(b *hclBlock) {
				b.setAlways("load_balancer_id", d.ref(balancer.ID))
				b.setAlways("name", listener.Name)
				b.setAlways("protocol", listener.Protocol)
				b.setAlways("protocol_port", listener.ProtocolPort)
				if listener.DefaultPoolID != "" {
					b.setAlways("default_pool_id", d.ref(listener.DefaultPoolID))
				}
				b.set("connection_limit", listener.ConnectionLimit)
				b.set("tls_certificate_ref", listener.TLSCertificateRef)
				// A certificate is not exported: its private key never leaves the
				// platform. The listener keeps pointing at the imported one.
				if listener.TLSCertificateRef != "" {
					b.comment = "tls_certificate_ref names an imported certificate, not exported: its private key\n" +
						"cannot be read back. Declare a nemau_load_balancer_certificate (with name_prefix and\n" +
						"create_before_destroy for renewals) to manage it here."
				}
			})
		}
	}
}

func renderClusters(d *hclDocument, found inventory) {
	for _, cluster := range found.Clusters {
		topology := cluster.Spec.Topology
		clusterName := d.name("nemau_kubernetes_cluster", cluster.Name)
		d.add("nemau_kubernetes_cluster", clusterName, cluster.Name, func(b *hclBlock) {
			b.setAlways("name", cluster.Name)
			b.setAlways("kubernetes_version", topology.Version)
			b.set("cluster_class", topology.Class)
			if topology.ControlPlaneFlavorID != "" {
				b.setAlways("control_plane_flavor_id", topology.ControlPlaneFlavorID)
			} else {
				b.setAlways("control_plane_vcpus", topology.ControlPlaneSizing.VCPUs)
				b.setAlways("control_plane_ram_mb", topology.ControlPlaneSizing.RAMMB)
				b.setAlways("control_plane_disk_gb", topology.ControlPlaneSizing.DiskGB)
			}
			b.set("control_plane_replicas", int(topology.ControlPlaneReplicas))
		})

		for _, pool := range topology.NodePools {
			poolName := d.name("nemau_kubernetes_node_pool", cluster.Name+"_"+pool.Name)
			d.add("nemau_kubernetes_node_pool", poolName, cluster.Name+":"+pool.Name, func(b *hclBlock) {
				b.setAlways("cluster_name", hclRef("nemau_kubernetes_cluster."+clusterName+".name"))
				b.setAlways("name", pool.Name)
				b.setAlways("node_class", pool.Class)
				if pool.FlavorID != "" {
					b.setAlways("flavor_id", pool.FlavorID)
				} else {
					b.setAlways("vcpus", pool.VCPUs)
					b.setAlways("ram_mb", pool.RAMMB)
					b.setAlways("disk_gb", pool.DiskGB)
				}
				b.set("replicas", int(pool.Replicas))
				b.set("autoscaler_min", int(pool.AutoscalerMin))
				b.set("autoscaler_max", int(pool.AutoscalerMax))
			})
		}
	}
}

func joinComma(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ", "
		}
		out += item
	}
	return out
}
