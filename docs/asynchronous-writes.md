# Asynchronous writes

Most writes run as tasks on the platform. The API accepts the request, returns
a task id, and the task does the work in the background. This covers creating,
changing and deleting instances, networks, volumes, containers, load balancers,
Kubernetes clusters and DNS zones.

A few writes act directly and return when the request completes, such as
uploading an object, creating an S3 key or importing a TLS certificate.
`--wait` and `--idempotency-key` have no effect on them.

## Waiting

By default the CLI waits for the task to finish, then prints the task, its
status and the id of the resource:

```console
$ nemau network vpc create prod
TASK                                   STATUS      RESOURCE
9b1e6d2a-3c4f-4a5b-8c7d-6e5f4a3b2c1d   completed   4c2a9e1f-8b3d-4f6a-a1c2-9e8d7b6a5f43
```

`--wait=false` returns as soon as the platform has accepted the request, and
prints the task id:

```console
$ nemau network vpc create prod --wait=false
TASK                                   STATUS
9b1e6d2a-3c4f-4a5b-8c7d-6e5f4a3b2c1d   pending
```

Follow the task with the `nemau task` commands:

```sh
nemau task wait <task-id>       # block until the task finishes
nemau task get <task-id>        # the task's current state
nemau task list <resource-id>   # the operations on one resource
```

There is no task list for a whole project or organization.

## Timeouts

`--timeout` bounds the whole command, including the wait. The default is 30
minutes, and `--timeout 0` removes the limit.

Some operations can take longer than the default, such as creating or upgrading
a Kubernetes cluster, or taking a snapshot of a large disk. Raise `--timeout`
for them, or start them with `--wait=false` and follow the task with
`nemau task wait <task-id> --timeout 2h`.

Once the platform has accepted the request, the task does not depend on the
CLI. If the CLI stops waiting, because of `--timeout` or Ctrl-C, the task keeps
running.

## Failed tasks

A failed task makes the command exit with status 1. The error names the task,
its type and the platform's message:

```text
error: nemau: task 7c1f0a9e-5d4b-4c3a-9e8f-1b2a3c4d5e6f (compute:delete) failed: ...
```

For most failures the platform replaces the underlying error with a fixed
message. Give the task id to support when you need help.

When the platform refuses a delete because something still depends on the
resource, its message names what to remove first, and the CLI adds the command
that does it:

```console
$ nemau compute instance delete <instance-id>
error: nemau: task 7c1f0a9e-5d4b-4c3a-9e8f-1b2a3c4d5e6f (compute:delete) failed: detach volume(s) data before deleting the instance
hint: detach the volume first: nemau storage volume detach <volume-id> <instance-id> (nemau storage volume list shows the ids)
```

## Retries

The CLI retries a request that fails with status `429`, a `5xx` status or a
network error, up to four attempts in total, with a random and growing delay
between them. When the response carries a `Retry-After` delay, the CLI waits at
least that long, up to 60 seconds. A longer delay is reported as an error at
once. When a request fails with `429` and the response named a delay, the CLI
adds a hint with that delay.

Reads are always retried. A write is retried only when it carries an
idempotency key, which every asynchronous write does. An object upload is sent
once.

## Idempotency keys

Every asynchronous write carries an `Idempotency-Key` header. By default the CLI
generates a new key for each command and keeps it across its own retries, so a
request that was rate limited or interrupted is never queued twice.

To make it safe to run the same command again, choose the key yourself with
`--idempotency-key`:

```sh
nemau network vpc create prod --idempotency-key deploy-4821-vpc
```

If that command is interrupted or its result is lost, run exactly the same line
again:

```console
$ nemau network vpc create prod --idempotency-key deploy-4821-vpc
replayed: the platform already had this request; task 9b1e6d2a-3c4f-4a5b-8c7d-6e5f4a3b2c1d is the original
TASK                                   STATUS      RESOURCE
9b1e6d2a-3c4f-4a5b-8c7d-6e5f4a3b2c1d   completed   4c2a9e1f-8b3d-4f6a-a1c2-9e8d7b6a5f43
```

The platform remembers a key for 24 hours and answers a repeat with the original
task. It refuses the same key with different parameters
(`422 idempotency_key_mismatch`), and the CLI then suggests sending the original
parameters again or choosing a new key.

A key is 1 to 128 characters among `A-Z`, `a-z`, `0-9`, `.`, `_`, `:` and `-`.
Each command makes at most one write, so use one key per command. Reads ignore
the key.
