package cli

import (
	"fmt"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
	"github.com/spf13/cobra"
)

// newWhoamiCommand reports the API the CLI targets, the credential in use and
// the project a command would act on.
func newWhoamiCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the API being targeted and the credential in use",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			// One unauthenticated call to prove the endpoint is reachable, then
			// one authenticated call to prove the credential is accepted.
			reachable, credential := "no", "refused"
			if _, err := c.Regions.List(ctx); err == nil {
				reachable = "yes"
			}
			if _, err := c.Compute.ListImages(ctx); err == nil {
				credential = "accepted"
			} else if nemau.IsUnauthorized(err) {
				credential = "refused (401)"
			} else if nemau.IsForbidden(err) {
				// Authenticated but without compute:Read, which is still a valid
				// credential.
				credential = "accepted (without compute:Read)"
			}

			// Resolved through the API rather than read from local state, so it
			// shows the project a command would actually use.
			project, projectSource := "none", "the credential"
			if current, err := c.Projects.Current(ctx); err == nil && current != nil {
				project = fmt.Sprintf("%s (%s)", current.Name, current.ID)
				_, projectSource = projectSelection()
			}

			type info struct {
				APIURL        string `json:"api_url"`
				Credential    string `json:"credential"`
				Reachable     string `json:"reachable"`
				Accepted      string `json:"accepted"`
				Project       string `json:"project"`
				ProjectSource string `json:"project_source"`
				SDKVersion    string `json:"sdk_version"`
			}
			data := info{
				APIURL:        c.BaseURL(),
				Credential:    c.DescribeCredential(),
				Reachable:     reachable,
				Accepted:      credential,
				Project:       project,
				ProjectSource: projectSource,
				SDKVersion:    nemau.Version,
			}
			return render(cmd, view{
				columns: []string{"FIELD", "VALUE"},
				rows: [][]string{
					{"api", data.APIURL},
					{"api reachable", data.Reachable},
					{"credential", data.Credential},
					{"credential accepted", data.Accepted},
					{"project", data.Project},
					{"project source", data.ProjectSource},
					{"sdk", data.SDKVersion},
				},
				data: data,
				id:   data.APIURL,
			})
		},
	}
}
