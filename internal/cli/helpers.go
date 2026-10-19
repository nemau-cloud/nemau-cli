package cli

import (
	"context"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
	"github.com/spf13/cobra"
)

// runCtx is the preamble every command runs: resolve the client and bound the
// call by --timeout. Keeping it in one place avoids a context that is never
// cancelled or a client built before the flags are parsed.
func runCtx(cmd *cobra.Command) (*nemau.Client, context.Context, context.CancelFunc, error) {
	c, err := client()
	if err != nil {
		return nil, nil, func() {}, err
	}
	ctx, cancel := commandContext(cmd)

	// A --project given as a name is resolved to an id here rather than in
	// client(): resolving it is an API call, which needs a context, and client()
	// should not make network calls.
	scoped, err := resolveProjectSelection(ctx, c)
	if err != nil {
		cancel()
		return nil, nil, func() {}, err
	}
	return scoped, ctx, cancel, nil
}

// forceJSON switches the output format for a command whose payload has no
// useful table form, such as a time series, a rate card or a preflight report.
//
// It overrides -o table rather than returning an error.
func forceJSON() {
	if g.output == "" || g.output == "table" {
		g.output = "json"
	}
}
