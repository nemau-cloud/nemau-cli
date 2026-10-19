# Output

## Formats

Every command prints an aligned table by default. The global flags `-o` and
`-q` change the format:

| Flag | Output |
| --- | --- |
| none | an aligned table |
| `-o json`, `--output json` | the result as JSON, with the API's field names, for `jq` and other tools |
| `-q`, `--quiet` | ids only, one per line |

When both are given, `-q` takes precedence.

`-q` is meant for chaining commands:

```sh
nemau compute instance list -q | xargs -n1 nemau compute instance stop
```

For a write, `-q` prints the id of the resource once the task completes. With
`--wait=false` it prints the task id.

## Standard output and standard error

Results go to standard output. Everything addressed to the reader goes to
standard error: warnings, hints, progress messages, and notes such as
`replayed: ...` or `listing stopped at --limit`. A redirection or a pipe
receives the result only.

In table format, an empty listing writes `no results` to standard error and
nothing to standard output, so a pipe receives an empty stream.

## Commands that always print JSON

Some results have no useful table form. These commands print JSON whatever
`-o` says:

- `nemau compute instance metrics`
- `nemau compute pricing`
- `nemau metering usage`, `nemau metering summary`, `nemau metering costs` and
  `nemau metering events`

## Secrets in the output

Some commands print a secret that the platform does not keep. It cannot be
shown again:

- `nemau compute keypair create` without `--public-key-file` prints the
  generated private key. With `--private-key-out` it writes the key to that
  file, with mode `0600`, instead.
- `nemau storage credentials create` prints the secret of the new S3 key.

`nemau k8s cluster kubeconfig` prints a kubeconfig with cluster-admin rights,
and reading it is audited. `--out` writes it to a file with mode `0600` instead
of standard output, so it does not pass through the terminal or shell history.
