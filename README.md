# nemau

`nemau` is the command-line client for the Nemau Cloud public API. It manages
instances, networks, load balancers, block and object storage, Kubernetes
clusters and DNS zones.

Building it requires Go 1.27 or later.

## Installation

Install it with Go:

```sh
go install github.com/nemau-cloud/nemau-cli/cmd/nemau@latest
```

The binary goes to `$(go env GOPATH)/bin`, or to `$GOBIN` when that is set.

You can also build it from a clone:

```sh
git clone https://github.com/nemau-cloud/nemau-cli.git
cd nemau-cli
go build -o nemau ./cmd/nemau
```

## Getting started

The CLI reads its credential from the environment and never writes it to disk.
Create an access key in the console, then export it:

```sh
export NEMAU_ACCESS_KEY_ID=AKIA...
export NEMAU_ACCESS_KEY_SECRET=...
```

A service account sets `NEMAU_TOKEN` instead. `NEMAU_API_URL` changes the API
origin, which defaults to `https://api.nemau-cloud.com`.

Check that the API answers and accepts the credential:

```console
$ nemau whoami
FIELD                 VALUE
api                   https://api.nemau-cloud.com
api reachable         yes
credential            access-key AKIA...
credential accepted   accepted
project               default (6f1d2c3b-0a9e-4b8c-9d7e-1a2b3c4d5e6f)
project source        the credential
sdk                   0.5.0
```

The organization comes from the credential. List its projects and choose the one
that later commands act on, by id, name or slug:

```console
$ nemau project list
    ID                                     NAME      REGION           STATE
*   6f1d2c3b-0a9e-4b8c-9d7e-1a2b3c4d5e6f   default   fr-occitanie-1   ready
    0b7e5a4c-2d1f-4e3a-8b9c-7d6e5f4a3b2c   staging   fr-occitanie-1   ready
$ nemau project use staging
project staging (0b7e5a4c-2d1f-4e3a-8b9c-7d6e5f4a3b2c) saved to /home/me/.config/nemau/config.json
ID                                     NAME      REGION           STATE
0b7e5a4c-2d1f-4e3a-8b9c-7d6e5f4a3b2c   staging   fr-occitanie-1   ready
```

Most writes run as tasks on the platform. By default the CLI waits for the task
to finish and prints the id of the resource:

```console
$ nemau network vpc create prod
TASK                                   STATUS      RESOURCE
9b1e6d2a-3c4f-4a5b-8c7d-6e5f4a3b2c1d   completed   4c2a9e1f-8b3d-4f6a-a1c2-9e8d7b6a5f43
```

`-o json` prints the result as JSON and `-q` prints ids only, one per line:

```sh
nemau compute instance list -o json | jq -r '.[] | select(.status == "running") | .name'
nemau compute instance list -q
```

## Documentation

- [User guide](docs/README.md): configuration, output formats, asynchronous
  writes, updates, data and infrastructure export, scripting and examples.
- [Command reference](docs/commands/README.md): every command and flag,
  generated from the help texts.
- [Nemau Cloud documentation](https://docs.nemau-cloud.com/en/tools/cli) for
  the CLI.

## Getting help

- `nemau <command> --help` describes a command and its flags.
- Report bugs and ask for features in the
  [issue tracker](https://github.com/nemau-cloud/nemau-cli/issues).
- Send general questions to contact@nemau-cloud.com.
- Report security issues privately, as described in [SECURITY.md](SECURITY.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) to build, test and propose a change.

## License

`nemau` is licensed under the Apache License 2.0. See [LICENSE](LICENSE) and
[NOTICE](NOTICE). Changes between releases are listed in
[CHANGELOG.md](CHANGELOG.md).
