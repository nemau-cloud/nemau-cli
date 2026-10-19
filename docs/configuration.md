# Configuration

## Credentials

`nemau` reads its credential from the environment. It never writes a
credential to disk.

An access key belongs to a user and is created in the console:

```sh
export NEMAU_ACCESS_KEY_ID=AKIA...
export NEMAU_ACCESS_KEY_SECRET=...
```

A service account authenticates with a token:

```sh
export NEMAU_TOKEN=...
```

When both an access key and a token are set, the access key is used.

An access key expires. The console issues it for 90 days unless you ask for
less, and 365 days is the longest it can last. A user can hold five active
keys. After the expiry date the API answers `401`, so create the next key
before then.

The S3 keys created by `nemau storage credentials create` are a different kind
of key. They give S3 tools access to one container and are described in
[Exporting data and infrastructure](export.md#with-your-own-s3-tools).

## API URL

The CLI sends its requests to `https://api.nemau-cloud.com`. To use another
origin, set `NEMAU_API_URL` or pass `--api-url`. The flag takes precedence over
the variable.

## Checking the setup

`nemau whoami` checks that the API answers, that it accepts the credential, and
which project commands will act on:

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

The `credential accepted` row reads `refused (401)` for a wrong or expired
credential. It reads `accepted (without compute:Read)` for a valid credential
whose policies do not allow reading compute resources.

## Selecting the project

The organization always comes from the credential. The project can be chosen:

```sh
nemau project list          # the organization's projects; * marks the target
nemau project use staging   # an id, a name or a slug
nemau project current       # the target project and what selected it
nemau project unset         # back to the credential's project
```

The CLI resolves the project in this order, most explicit first:

| Source | Scope |
| --- | --- |
| `--project` | one command |
| `NEMAU_PROJECT_ID` | a shell or a CI job |
| `nemau project use` | every later command, through the configuration file |
| none of the above | the project the credential carries |

The choice is local. Nothing is stored on the platform, so two shells can target
two projects with the same access key, and a CI job sets its target with the
environment variable.

`--project` and `NEMAU_PROJECT_ID` accept an id, a name or a slug. An id is used
as it is. A name or a slug costs one extra request to look up the id.

`nemau project use` checks the project against the API and saves its id, so a
renamed project keeps working. It warns when the project is not ready, because
such a project refuses every create. It also warns when `NEMAU_PROJECT_ID` names
another project, because the variable takes precedence over the saved choice.

A service account pinned to a project cannot leave it. The API answers `403`
and names the project the account is attached to.

## Configuration file

The only state the CLI keeps between commands is the selected project. It is
saved in `$XDG_CONFIG_HOME/nemau/config.json`, or in
`~/.config/nemau/config.json` when `XDG_CONFIG_HOME` is not set:

```json
{
  "project_id": "0b7e5a4c-2d1f-4e3a-8b9c-7d6e5f4a3b2c"
}
```

The CLI writes the file with mode `0600` and creates its directory with mode
`0700`. A file that cannot be read or parsed is ignored, and the next
`nemau project use` replaces it. Credentials stay in the environment, so
backups and synced dotfiles never contain them.

## Shell completion

`nemau completion <shell>` prints a completion script for bash, zsh, fish or
PowerShell:

```sh
nemau completion zsh  > "${fpath[1]}/_nemau"
nemau completion bash > /etc/bash_completion.d/nemau
```

`nemau completion <shell> --help` explains how to load the script in each
shell.
