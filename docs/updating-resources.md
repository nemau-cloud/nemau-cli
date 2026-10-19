# Updating resources

Names, descriptions and the settings the platform can reconfigure change in
place. Any other change means creating a new resource and deleting the old one.

## Update commands

An `update` command sends only the flags you pass. `--description ""` clears a
description, while leaving the flag out keeps it. Without any flag, the command
fails and lists the flags it accepts.

```sh
nemau network vpc update <network-id> --name prod --description "front segment"
nemau lb pool update <pool-id> --algorithm LEAST_CONNECTIONS
nemau storage container update backups --retention-days 90
nemau dns zone update example.com. --ttl 600
```

| Command | Flags |
| --- | --- |
| `nemau compute instance update` | `--name`, `--description` |
| `nemau network vpc update` | `--name`, `--description` |
| `nemau network subnet update` | `--name` |
| `nemau network security-group update` | `--name`, `--description` |
| `nemau network floating-ip update` | `--description` |
| `nemau storage volume update` | `--name`, `--description` |
| `nemau storage container update` | `--versioning`, `--retention-days` |
| `nemau lb instance update` | `--name`, `--description` |
| `nemau lb listener update` | `--name`, `--description`, `--connection-limit`, `--tls-certificate` |
| `nemau lb pool update` | `--name`, `--description`, `--algorithm` |
| `nemau lb pool update-member` | `--name`, `--weight` |
| `nemau lb pool update-monitor` | `--delay`, `--probe-timeout`, `--max-retries`, `--expected-codes` |
| `nemau dns zone update` | `--description`, `--email`, `--ttl` |

`nemau dns record update <zone> <recordset-id>` changes a record set's values
(`--record`, repeatable), its `--ttl` and its `--description`.

Renaming a network does not rebuild it. The network keeps its identifier, and
the instances attached to it are untouched. Renaming an instance does not
reboot it.

## Resizing an instance

`nemau compute instance resize` takes all three of `--vcpus`, `--ram` and
`--disk`:

```sh
nemau compute instance resize <instance-id> --vcpus 4 --ram 8192 --disk 80
```

Raising only the disk is applied online, and the instance keeps running.
Changing the vCPUs or the memory power-cycles the instance, because the
hypervisor would otherwise leave the change pending until the next boot.

## Other changes in place

- `nemau storage volume extend <volume-id> --size <GB>` grows a volume. A volume
  never shrinks.
- `nemau compute instance set-security-groups <instance-id> --security-group <group-id>`
  replaces the instance's security groups with the ones given. `--clear`
  removes them all.
- `nemau network floating-ip associate` attaches a floating IP to an instance
  or a load balancer, and `disassociate` detaches it.
- `nemau network security-group enable-egress-filtering` converts a group
  created before egress filtering. It cannot be undone; see
  [Examples](examples.md#restrict-outbound-traffic).
- `nemau k8s cluster scale-control-plane`, `nemau k8s node-pool scale` and
  `nemau k8s node-pool autoscaler` change the size of a cluster.
  `nemau k8s cluster upgrade` changes its Kubernetes version.
- `nemau lb listener update <listener-id> --tls-certificate <certificate-id>`
  switches the certificate a listener serves.

## Settings fixed at creation

- A load balancer's tier (`--flavor`) cannot be changed.
- A container cannot be renamed. Object lock and the retention mode are set at
  creation only. The retention period can be extended with
  `--retention-days`, never shortened.
- A TLS certificate cannot be modified. To renew one, import the new
  certificate and switch the listener to it, as shown in
  [Examples](examples.md#serve-https-from-a-load-balancer).
