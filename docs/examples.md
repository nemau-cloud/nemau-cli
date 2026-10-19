# Examples

Each example assumes credentials in the environment and a selected project, as
described in [Configuration](configuration.md). Values in angle brackets, such
as `<instance-id>`, stand for ids printed by earlier commands.

## Create an instance and reach it

Generate an SSH keypair. The platform returns the private key once and does not
keep it:

```sh
nemau compute keypair create deploy --private-key-out ~/.ssh/nemau_deploy
```

Pick an image, create the instance on a network, then give it a public address:

```sh
nemau compute image
nemau compute instance create \
  --name web-1 --image <image-id> \
  --vcpus 2 --ram 4096 --disk 40 \
  --network "$(nemau network vpc list -q | head -1)" \
  --key-name deploy

nemau network floating-ip allocate -q > fip.id
nemau network floating-ip associate "$(cat fip.id)" <instance-id> --target instance
```

The size is either `--flavor` or all three of `--vcpus`, `--ram` and `--disk`.

## Reach an instance over IPv6

`nemau network capabilities` shows whether the region offers IPv6.

An IPv6 floating IP is translated to the instance's private IPv4 address on the
exit node, so the guest needs no IPv6 setup. A target holds at most one IPv6
floating IP:

```sh
nemau network floating-ip allocate --ip-version 6 -q > fip6.id
nemau network floating-ip associate "$(cat fip6.id)" <instance-id> --target instance
```

To make a network dual-stack instead, add an IPv6 subnet. The platform assigns
the /64, and the network must already have an IPv4 subnet. Instances created on
the network afterwards get a public IPv6 address and need a security group:

```sh
nemau network subnet create <network-id> app-v6 --ip-version 6
```

## Restrict outbound traffic

A new security group filters outbound traffic. It starts with two egress rules
that allow everything, one for IPv4 and one for IPv6. Replace them with the
destinations the instance needs:

```sh
nemau network security-group rules <group-id>
nemau network security-group add-rule <group-id> --direction egress \
  --protocol udp --port-min 53 --port-max 53 --remote 0.0.0.0/0
nemau network security-group add-rule <group-id> --direction egress \
  --protocol tcp --port-min 443 --port-max 443 --remote 0.0.0.0/0
nemau network security-group delete-rule <group-id> <rule-id>
```

Keep DNS open, as above, or name resolution stops. DHCP needs no rule. An
instance filters its outbound traffic only when every one of its groups does.

A group created before egress filtering existed is listed as `not filtered`:
all outbound traffic is allowed and its egress rules have no effect.
`enable-egress-filtering` converts it. The conversion adds two egress rules that
allow everything, so traffic is unchanged until you remove or narrow them. It
cannot be undone:

```sh
nemau network security-group enable-egress-filtering <group-id>
```

## Create a Kubernetes cluster

Clusters are addressed by name. Without `--node-pool`, a cluster gets one pool
named `workers` with 3 nodes of 2 vCPU, 4096 MB and 20 GB, class
`default-worker`. Each `--node-pool` declares a pool instead; repeat the flag
for several:

```sh
nemau k8s versions
nemau k8s cluster create prod --version v1.31.0 \
  --control-plane-vcpus 2 --control-plane-ram 4096 --control-plane-disk 20 \
  --control-plane-replicas 3 \
  --node-pool name=workers,replicas=3,vcpus=4,ram=8192,disk=50 \
  --node-pool name=batch,autoscaler-min=1,autoscaler-max=10,flavor=<flavor-id> \
  --timeout 1h
```

A pool takes a `name`, which must be a DNS label, and optionally a `class`,
`replicas` (0 to 100), a size (`flavor`, or all three of `vcpus`, `ram` and
`disk`) and the autoscaler bounds `autoscaler-min` and `autoscaler-max`. A pool
given no size takes `--worker-flavor` when it is set.

Add a pool to a running cluster:

```sh
nemau k8s node-pool add prod gpu --class default-worker --flavor <flavor-id> --replicas 1
```

Write the kubeconfig to a file instead of the terminal. It grants
cluster-admin rights:

```sh
nemau k8s cluster kubeconfig prod --out ~/.kube/nemau-prod.yaml
export KUBECONFIG=~/.kube/nemau-prod.yaml
```

## Upgrade a Kubernetes cluster

One task runs the preflight checks, opens the maintenance window and starts the
rolling upgrade. A preflight with blocking checks fails the task:

```sh
nemau k8s versions
nemau k8s cluster upgrade prod --version 1.31.2 --timeout 2h
nemau k8s cluster get prod
```

When the task completes, the nodes are still being replaced one by one.
`nemau k8s cluster get` shows the progress. The upgrade requires the
`kubernetes-guided-upgrades` organization feature.

## Snapshot an instance before a change

```sh
nemau compute snapshot create <instance-id> before-upgrade --timeout 2h
nemau compute snapshot list --instance <instance-id>
nemau compute snapshot restore <snapshot-id>
```

A snapshot copies the system disk to the object store without stopping the
instance. It is billed on the compressed bytes stored. Attached volumes are not
part of a snapshot.

Restoring swaps the system disk: the instance stops only for the swap, and
everything written to the disk since the snapshot is lost. Addresses, security
groups and attached volumes are kept. Deleting an instance deletes its
snapshots.

## Serve HTTPS from a load balancer

Import the certificate and its private key, then create a listener that
terminates TLS. The private key is never shown again:

```sh
nemau lb certificate import --name www \
  --certificate fullchain.pem --private-key privkey.pem
nemau lb listener create <lb-id> https --protocol TERMINATED_HTTPS --port 443 \
  --tls-certificate <certificate-id>
```

A certificate cannot be modified. To renew it, import the new one, switch the
listener to it, and delete the old one. The appliance reloads without dropping
connections:

```sh
nemau lb certificate import --name www-2027 \
  --certificate fullchain.pem --private-key privkey.pem
nemau lb listener update <listener-id> --tls-certificate <new-certificate-id>
nemau lb certificate delete <old-certificate-id>
```

`nemau lb certificate list` shows each certificate's expiry and the listeners
that serve it.

## Verify a DNS zone

A new zone is not served until you prove that you control its domain. It is
created as `PENDING_VERIFICATION`, and the CLI prints the TXT record to publish
at the domain's current DNS host:

```console
$ nemau dns zone create example.com --email dns@example.com
...
zone example.com. is pending verification. Publish this record at the domain's current DNS host:

  _nemau-challenge.example.com.  TXT  "nemau-verification=..."

then run: nemau dns zone verify 3f1c9a2e-7b4d-4e8a-9c61-2d5f0b8e4a17
```

Wait until the record is visible, then verify the zone:

```sh
dig TXT _nemau-challenge.example.com +short
nemau dns zone verify example.com.
```

Verification fails, and the zone stays pending, while the record is absent or
wrong. A record published a moment ago can take a while to reach the domain's
nameservers; run the command again later. `nemau dns zone get example.com`
shows the record again.

Wherever a command takes a `<zone>`, the zone's name works as well as its id,
with or without the trailing dot, provided it matches exactly one zone. Record
sets can only be written to a verified zone. Once the zone is active, delegate
the domain to the zone's `NS` records at your registrar.

## Keep immutable backups

```sh
nemau storage container create backups --object-lock --retention-days 30
nemau storage container protection backups
```

With `--object-lock`, no stored version can be deleted during the retention
period. A compromised S3 key can add a delete marker. It cannot remove a locked
version. Object lock can only be set when the container is created.

| Mode | Who can lift the lock |
| --- | --- |
| `GOVERNANCE` (default) | the platform, on a justified request |
| `COMPLIANCE` | nobody before it expires, the platform included |

`nemau storage container protection` asks the object store what it enforces.
The container listing shows what was requested at creation. Use `protection`
to check immutability, for example during an audit.

Versioning alone does not make a container immutable. Without the lock, a
delete still destroys the version.

A copy into a locked container is locked with that container's default
retention, whatever the source's retention. The object store copies the bytes
itself, up to 5 GiB per object:

```sh
nemau storage object copy photos 2026/holiday.jpg backups
```

## List a large container

`nemau storage object list` follows the platform's page cursor, at most 1000
objects per request, until the container is exhausted. `--limit` stops early
and prints the cursor to continue from on standard error:

```sh
nemau storage object list photos --prefix 2026/ -q | wc -l
nemau storage object list photos --limit 500
# listing stopped at --limit; continue with --marker <cursor>
nemau storage object list photos --limit 500 --marker <cursor>
```

## Read an instance's metrics

`nemau compute instance metrics` prints a time series as JSON. `--period` takes
`1h` (the default), `6h`, `24h` or `7d`:

```sh
nemau compute instance metrics <instance-id> --period 24h
```
