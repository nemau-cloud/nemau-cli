# Contributing to nemau

Bug reports, fixes and improvements are welcome.

## Proposing a change

Report a bug or suggest a change in the
[issue tracker](https://github.com/nemau-cloud/nemau-cli/issues). For a large
change, open an issue first so the approach can be discussed before you write
the code.

Send changes as a pull request against `main`. Keep each pull request to one
topic, and include tests for new behaviour.

Do not report security issues in the tracker. See [SECURITY.md](SECURITY.md).

## Building and testing

The CLI needs Go 1.27 or later. Before sending a change, run:

```sh
go build ./... && go vet ./... && go test ./... && gofmt -l .
```

`gofmt -l .` must print nothing. CI also runs the tests with `-race`.

`make build` writes `bin/nemau` with the version taken from the latest git
tag.

The CLI is built on [nemau-sdk-go](https://github.com/nemau-cloud/nemau-sdk-go). The
CLI handles arguments, output and waiting for tasks. Requests to the API and the
handling of its responses belong in the SDK.

## Command reference

The pages in [docs/commands](docs/commands/README.md) are generated from the
command definitions. After changing a command, a flag or a help text,
regenerate them from the repository root and commit the result with the
change:

```sh
go run ./internal/tools/gendocs
```

`go test ./...` fails when the committed pages are out of date.

Help texts, documentation and messages are written in English.

## Commit messages

Write a short subject line that starts with a capital letter and has no
trailing period, such as `Describe the export files in the help text`. When
the reason for the change is not obvious, explain it in the body after a blank
line.

## Changelog

Add a line for every user-visible change to [CHANGELOG.md](CHANGELOG.md), under
the `Unreleased` heading, in the section that fits, such as `Added`, `Changed`
or `Fixed`. The file follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
