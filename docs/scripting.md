# Scripting

`nemau` never prompts. Every command, deletes included, runs without asking for
confirmation, so the same command line works in a terminal, a script or a CI
job.

## Exit status

| Status | Meaning |
| --- | --- |
| `0` | The command succeeded. |
| `1` | The command failed: invalid arguments or flags, a request the API refused, a failed task, an expired `--timeout`, or an interruption with Ctrl-C. |

The error goes to standard error, prefixed with `error:`. When the CLI knows
what to do next, it adds a line that starts with `hint:`.

Some commands use the exit status as their result:

- `nemau region status <slug>` exits with status 1 when the region is
  unhealthy, so it can be used in a monitoring script.
- `nemau storage export` tries every object, then exits with status 1 if any of
  them could not be fetched.

## Standard streams

Results go to standard output and messages to standard error, so a pipe or a
redirection receives the result only. See [Output](output.md) for the formats.

Capture the id of a new resource with `-q`:

```sh
network=$(nemau network vpc create prod -q)
nemau network subnet create "$network" app --cidr 10.0.1.0/24
```

Use `-o json` with `jq` to read other fields:

```sh
nemau compute instance list -o json | jq -r '.[] | select(.status == "running") | .id'
```

`nemau storage object put <container> <key> -` reads the object from standard
input. The content is buffered in memory, up to 100 MiB.
`nemau storage object get <container> <key>` without a file name writes the
object to standard output.

## CI jobs

Store the credential in masked CI variables (`NEMAU_ACCESS_KEY_ID` and
`NEMAU_ACCESS_KEY_SECRET`, or `NEMAU_TOKEN`) and set `NEMAU_PROJECT_ID` to the
project the job deploys to. The variable takes precedence over a project saved
with `nemau project use`. See [Configuration](configuration.md#selecting-the-project).

A service account pinned to a project cannot act on another one, which keeps a
job from deploying to the wrong project.

## Safe re-runs

Pass an idempotency key derived from the run, so that retrying a failed job
does not create resources twice. Each command makes at most one write, so give
each command its own key:

```sh
nemau network vpc create prod --idempotency-key "deploy-$CI_PIPELINE_ID-vpc"
nemau storage volume create data --size 100 --idempotency-key "deploy-$CI_PIPELINE_ID-volume"
```

The platform keeps a key for 24 hours. See
[Asynchronous writes](asynchronous-writes.md#idempotency-keys) for the rules.

## Long operations

`--timeout` bounds each command, 30 minutes by default. For operations that can
take longer, start the task without waiting and wait for it in a later step:

```sh
nemau k8s cluster create prod --version v1.31.0 \
  --control-plane-vcpus 2 --control-plane-ram 4096 --control-plane-disk 20 \
  --wait=false -q > task.id
nemau task wait "$(cat task.id)" --timeout 1h
```
