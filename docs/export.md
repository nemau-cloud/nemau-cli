# Exporting data and infrastructure

`nemau storage export` downloads the contents of object containers, and
`nemau export` writes the infrastructure as Terraform. Both use the public API
only, so you can run them without help from the platform, for example to move
to another provider.

## Object storage

```sh
nemau storage export photos ./backup/photos
```

Each object is fetched through a signed URL, straight from the object store.
The bytes do not go through the API, so the API's 100 MiB per-object limit does
not apply.

The key hierarchy is reproduced on disk. Downloads run in parallel
(`--parallel`, 4 by default). A file already present with the right size is
skipped, so an interrupted export resumes where it stopped; `--overwrite`
fetches every object again. `--prefix` limits the export to the keys starting
with a prefix.

An incomplete export exits with status 1, so a script cannot mistake missing
objects for success.

For a single object, or to use your own tools, get a signed URL:

```sh
curl -o photo.jpg "$(nemau storage object presign photos photo.jpg -q)"
```

The URL is a bearer token for that object until it expires: 15 minutes by
default, one hour at most (`--expires-in`). Do not put it in a log.

### With your own S3 tools

For a full migration, an S3 key scoped to one container is more practical than
one URL per object. `--format env` prints the variables the AWS CLI reads:

```sh
eval "$(nemau storage credentials create photos --access read-only --format env)"
aws --endpoint-url "$AWS_ENDPOINT_URL" s3 sync "s3://$NEMAU_S3_BUCKET" ./backup
```

`--format rclone` prints an rclone remote named `nemau`, and the bucket to sync
on standard error:

```sh
nemau storage credentials create photos --access read-only --format rclone >> ~/.config/rclone/rclone.conf
rclone sync nemau:<s3-bucket> ./backup --progress
```

S3 tools address a container by its S3 bucket name, which differs from the
container name: the object store knows each container under an opaque name of
its own. `nemau storage credentials create` prints it (`s3_bucket`, or
`NEMAU_S3_BUCKET` with `--format env`), and `nemau storage container list` shows
it for every container.

A key belongs to one container and is `read-only` or `read-write`. It cannot
see another container or change container settings. A container can hold
several keys, up to a limit set by the platform; when the limit is reached, the
CLI names the command that revokes an unused key. The secret is shown once,
when the key is created.

A key stays valid until it is revoked, unless you give it an expiry with
`--expires-in 24h` or `--expires-at 2026-12-31T23:59:00Z` (one or the other).
`nemau storage credentials list photos` shows a container's keys, and
`nemau storage credentials revoke photos <access-key-id>` revokes one. Key
creation and revocation are audited, and deleting the container revokes its
keys.
These commands need the `storage:ManageCredentials` permission.

When the platform has no S3 endpoint reachable from outside, as in a
demonstration environment, the CLI says so and does not set
`AWS_ENDPOINT_URL`.

## Infrastructure

```sh
nemau export ./infrastructure
```

The command writes three files:

| File | Contents |
| --- | --- |
| `nemau.tf` | the resources, as Terraform configuration |
| `imports.tf` | Terraform `import` blocks (Terraform 1.5 or later) |
| `inventory.json` | the inventory as the API returned it |

The directory can also be given with `--out`, and defaults to the current
directory. `--imports=false` skips `imports.tf`.

Dependencies between resources are written as Terraform references:

```hcl
resource "nemau_subnet" "app" {
  network_id = nemau_network.app.id
  name       = "app"
  cidr       = "10.42.0.0/24"
}
```

The configuration can therefore be applied to an empty project, or adapted to
another provider. Applied to its original project, the `import` blocks in
`imports.tf` make Terraform adopt the existing resources instead of creating
them a second time.

A security group that filters outbound traffic creates its two default egress
rules (`0.0.0.0/0` and `::/0`) itself, so they are not exported: declaring them
would fail on apply. To manage them with Terraform, import them with an
`import` block whose id is `<group id>:<rule id>`
(`nemau network security-group rules <group id>` lists the rule ids), then
remove the resource block to delete them.

The export runs on your machine and asks the platform to generate nothing. It
writes no secret: no private key, no kubeconfig, no object content. SSH keys
appear only as their public half.

When a service cannot be read, because the credential lacks a permission or
the feature is not enabled, the CLI prints `skipped:` with the reason on
standard error and exports the rest. An export is often run with a narrowly
scoped credential.

## Instance disks

The API does not export disk images, and the platform does not import them
either: a disk image supplied by a customer would have to be parsed on a
hypervisor, which is a security risk.

Copy the data from inside the instance instead, for example with rclone and a
read-write S3 key of the target container:

```sh
# on your machine: print an rclone remote for the "backups" container
nemau storage credentials create backups --access read-write --format rclone

# on the instance, with that remote in ~/.config/rclone/rclone.conf
rclone sync /var/lib/data nemau:<s3-bucket>/server-1 --progress
```

The data of an instance that no longer boots cannot be copied out this way.
Back up from the guest while it runs.
