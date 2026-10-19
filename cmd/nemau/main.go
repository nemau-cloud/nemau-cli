// Command nemau is the Nemau Cloud command line client.
package main

import (
	"fmt"
	"os"

	"github.com/nemau-cloud/nemau-cli/internal/cli"
)

// version is stamped at build time:
//
//	go build -ldflags "-X main.version=$(git describe --tags)" ./cmd/nemau
var version = "dev"

func main() {
	if err := cli.Execute(version); err != nil {
		// cobra has already printed usage for a flag or argument error; this is
		// the runtime failure path, so only the message is added.
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
