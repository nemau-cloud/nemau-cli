// Package cli builds the nemau command tree.
//
// Every command is a thin shell over github.com/nemau-cloud/nemau-sdk-go: the CLI
// owns argument parsing, output shape and the wait-for-completion behaviour,
// and nothing else. Anything that looks like API knowledge belongs in the SDK.
package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
	"github.com/spf13/cobra"
)

// globals holds the flags every command shares.
type globals struct {
	apiURL  string
	project string
	output  string
	wait    bool
	timeout time.Duration
	quiet   bool
	// idempotencyKey, when set, is sent on the write the command makes instead of
	// a generated one. Reads ignore it.
	idempotencyKey string

	client *nemau.Client
}

var g = &globals{}

// Execute runs the command tree.
func Execute(version string) error {
	root := newRootCommand(version)
	// A cancelled context is the user pressing Ctrl-C, not a program error:
	// report nothing and exit non-zero through the caller.
	err := root.ExecuteContext(interruptContext())
	if errors.Is(err, context.Canceled) {
		return errors.New("interrupted")
	}
	return explainError(err)
}

// explainError adds the next step to the refusals a user can act on. The error
// itself is untouched: errors.Is/As and the platform's own message survive.
func explainError(err error) error {
	switch {
	case err == nil:
		return nil
	case nemau.IsIdempotencyKeyMismatch(err):
		return &hintedError{err: err, hint: "this idempotency key was already used for a different request; " +
			"send the original parameters again, or pick a new --idempotency-key"}
	}
	var apiErr *nemau.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests && apiErr.RetryAfter > 0 {
		return &hintedError{err: err, hint: fmt.Sprintf("rate limited; try again in %s", apiErr.RetryAfter.Round(time.Second))}
	}
	return err
}

func interruptContext() context.Context {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	// stop is not called: the context lives as long as the process, which exits
	// right after Execute returns.
	_ = stop
	return ctx
}

// NewRootCommand returns the command tree without running it. The reference
// generator in internal/tools/gendocs walks it to write docs/commands.
func NewRootCommand(version string) *cobra.Command {
	return newRootCommand(version)
}

func newRootCommand(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "nemau",
		Short: "Nemau Cloud command-line client",
		Long: `nemau drives the Nemau Cloud public API: compute, network, storage,
managed Kubernetes and DNS.

Authentication:

    export NEMAU_ACCESS_KEY_ID=...
    export NEMAU_ACCESS_KEY_SECRET=...
    # or, for a service account:
    export NEMAU_TOKEN=...
    export NEMAU_API_URL=https://api.nemau-cloud.com   # optional

Project: "nemau project list" to see them, "nemau project use <project>" to
pick one. The choice is saved locally and applies to later commands; --project
or NEMAU_PROJECT_ID override it for one invocation. With none of those, the
project the credential carries applies.

Writes are asynchronous: by default the CLI waits for the operation to finish.
--wait=false returns immediately, printing the task id.

Every write carries an idempotency key, generated per command and kept across
the CLI's own retries, so a rate-limited or interrupted request is never
queued twice. To make a re-run of the same command safe too, pass your own
with --idempotency-key: the platform then returns the original task instead of
creating the resource again (for 24 hours).`,
		Version:      version,
		SilenceUsage: true,
		// cobra would otherwise prefix the message with its own "Error:", which
		// main.go already prints.
		SilenceErrors: true,
	}

	pf := root.PersistentFlags()
	pf.StringVar(&g.apiURL, "api-url", "", "API origin (default: $NEMAU_API_URL, then "+nemau.DefaultBaseURL+")")
	pf.StringVar(&g.project, "project", "", "target project, by id or by name (default: $NEMAU_PROJECT_ID, then the saved project)")
	pf.StringVarP(&g.output, "output", "o", "table", "output format: table, json")
	pf.BoolVar(&g.wait, "wait", true, "wait for asynchronous operations to finish")
	pf.DurationVar(&g.timeout, "timeout", 30*time.Minute, "how long to wait for one operation")
	pf.BoolVarP(&g.quiet, "quiet", "q", false, "print ids only")
	pf.StringVar(&g.idempotencyKey, "idempotency-key", "", "key making a write safe to repeat: 1-128 characters of A-Z a-z 0-9 . _ : - (default: generated)")

	root.AddCommand(
		newWhoamiCommand(),
		newProjectCommand(),
		newQuotaCommand(),
		newExportCommand(),
		newRegionCommand(),
		newComputeCommand(),
		newNetworkCommand(),
		newLoadBalancerCommand(),
		newStorageCommand(),
		newKubernetesCommand(),
		newDNSCommand(),
		newMeteringCommand(),
		newTaskCommand(),
	)
	return root
}

// client builds the SDK client on first use.
//
// It is built lazily so that `nemau --help` and `nemau completion` work without
// credentials. A missing credential is reported by the command that calls the
// API.
func client() (*nemau.Client, error) {
	if g.client != nil {
		return g.client, nil
	}
	opts := []nemau.Option{nemau.WithUserAgent("nemau-cli")}
	if g.apiURL != "" {
		opts = append(opts, nemau.WithBaseURL(g.apiURL))
	}
	if project := selectedProject(); project != "" {
		opts = append(opts, nemau.WithProject(project))
	}
	c, err := nemau.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	g.client = c
	return c, nil
}

// selectedProject resolves the project this invocation targets, most explicit
// source first.
//
// The order is the one gcloud and scw use: the flag, then the environment, then
// the saved project. With none of them the credential's own project applies, so
// an empty result is valid and not an error.
//
// NEMAU_PROJECT_ID is read here rather than by the SDK so that the whole
// precedence, including the config file the SDK does not know about, is decided
// in one place.
func selectedProject() string {
	if g.project != "" {
		return g.project
	}
	if env := strings.TrimSpace(os.Getenv(nemau.EnvProject)); env != "" {
		return env
	}
	return loadConfig().ProjectID
}

// commandContext bounds a command by --timeout, on top of the interrupt context.
//
// It also carries --idempotency-key: the SDK reads it from the context when the
// command makes its write. No command makes two writes, so one key never stands
// for two different requests.
func commandContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	parent := cmd.Context()
	if g.idempotencyKey != "" {
		parent = nemau.WithIdempotencyKey(parent, g.idempotencyKey)
	}
	if g.timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, g.timeout)
}

// hintedError adds a next step to an error without hiding the error itself:
// errors.Is/As still see the original, so exit handling and tests keep working.
type hintedError struct {
	err  error
	hint string
}

func (e *hintedError) Error() string { return e.err.Error() + "\nhint: " + e.hint }
func (e *hintedError) Unwrap() error { return e.err }

// withInUseHint tells the user what to do when a delete was refused because a
// dependant still uses the resource. The platform's message names the dependant;
// the hint names the commands that remove it. Any other error passes through.
func withInUseHint(err error, hint string) error {
	if nemau.IsResourceInUse(err) {
		return &hintedError{err: err, hint: hint}
	}
	return err
}

// finish reports an asynchronous write.
//
// With --wait (the default) it blocks until the task is terminal and prints the
// resource id; without it, it prints the task id and returns immediately. Every
// write command goes through it, so they all behave the same way.
func finish(cmd *cobra.Command, ctx context.Context, ref *nemau.TaskRef, resultKeys ...string) error {
	_, err := finishID(cmd, ctx, ref, resultKeys...)
	return err
}

// finishID is [finish] for a command that has more to say once the operation
// is done: it also returns the resource id, empty with --wait=false.
func finishID(cmd *cobra.Command, ctx context.Context, ref *nemau.TaskRef, resultKeys ...string) (string, error) {
	c, err := client()
	if err != nil {
		return "", err
	}
	if ref.Replayed && !g.quiet {
		fmt.Fprintf(cmd.ErrOrStderr(), "replayed: the platform already had this request; task %s is the original\n", ref.TaskID)
	}
	if !g.wait {
		return "", render(cmd, view{
			columns: []string{"TASK", "STATUS"},
			rows:    [][]string{{ref.TaskID, ref.Status}},
			data:    ref,
			id:      ref.TaskID,
		})
	}

	task, err := c.WaitFor(ctx, ref)
	if err != nil {
		return "", err
	}
	id := ""
	if task.ResourceID != nil {
		id = *task.ResourceID
	}
	if id == "" {
		id = task.ResultString(resultKeys...)
	}
	return id, render(cmd, view{
		columns: []string{"TASK", "STATUS", "RESOURCE"},
		rows:    [][]string{{task.ID, task.Status, id}},
		data:    task,
		id:      id,
	})
}

// requireArgs is cobra.ExactArgs with a message that names the usage.
func requireArgs(n int, usage string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return fmt.Errorf("expected %d argument(s): %s", n, usage)
		}
		return nil
	}
}
