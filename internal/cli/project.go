package cli

import (
	"context"
	"fmt"
	"os"
	"regexp"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
	"github.com/spf13/cobra"
)

func newProjectCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "project",
		Aliases: []string{"projects", "p"},
		Short:   "Select and inspect the target project",
		Long: `Select and inspect the target project.

The project is resolved in this order, most explicit first:

    --project              for a single command
    NEMAU_PROJECT_ID       for a shell or a CI job
    nemau project use      saved in ~/.config/nemau/config.json
    (nothing)              the project the credential itself carries

Nothing is stored on the platform: two shells can target two projects with the
same access key. A service account pinned to a project cannot leave it: the
API answers 403.`,
	}
	cmd.AddCommand(
		projectListCommand(),
		projectCurrentCommand(),
		projectUseCommand(),
		projectUnsetCommand(),
	)
	return cmd
}

func projectListCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the organization's projects",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			list, err := c.Projects.List(ctx)
			if err != nil {
				return err
			}
			v := view{columns: []string{"", "ID", "NAME", "REGION", "STATE"}, data: list}
			for _, project := range list.Projects {
				marker := " "
				if project.ID == list.CurrentProjectID {
					marker = "*"
				}
				v.rows = append(v.rows, []string{marker, project.ID, project.Name,
					project.Region, projectState(project)})
				v.ids = append(v.ids, project.ID)
			}
			if err := render(cmd, v); err != nil {
				return err
			}
			// Warn when the credential points at a project outside this list.
			if list.CurrentProjectID != "" && !containsProject(list.Projects, list.CurrentProjectID) &&
				!g.quiet && g.output != "json" {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"warning: the target project %s does not belong to this organization\n",
					list.CurrentProjectID)
			}
			return nil
		},
	}
}

// projectState collapses the two readiness flags into one column. A project
// that is inactive or still provisioning is listed but refuses every write.
func projectState(project nemau.Project) string {
	switch {
	case !project.IsActive:
		return "inactive"
	case project.ProvisioningStatus != "active":
		return project.ProvisioningStatus
	default:
		return "ready"
	}
}

func containsProject(projects []nemau.Project, id string) bool {
	for _, project := range projects {
		if project.ID == id {
			return true
		}
	}
	return false
}

func projectCurrentCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Show the target project and where that choice comes from",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			selection, source := projectSelection()

			current, err := c.Projects.Current(ctx)
			if err != nil {
				return err
			}
			if current == nil {
				fmt.Fprintln(cmd.ErrOrStderr(),
					`no target project: pick one with "nemau project use <project>"`)
				return nil
			}

			type currentProject struct {
				nemau.Project
				Source    string `json:"source"`
				Selection string `json:"selection"`
			}
			data := currentProject{Project: *current, Source: source, Selection: selection}
			return render(cmd, view{
				columns: []string{"FIELD", "VALUE"},
				rows: [][]string{
					{"id", current.ID},
					{"name", current.Name},
					{"region", current.Region},
					{"state", projectState(*current)},
					{"selected by", source},
				},
				data: data,
				id:   current.ID,
			})
		},
	}
}

// projectSelection reports the selected value and where it came from.
func projectSelection() (selection, source string) {
	if g.project != "" {
		return g.project, "--project"
	}
	if env := os.Getenv(nemau.EnvProject); env != "" {
		return env, nemau.EnvProject
	}
	if stored := loadConfig().ProjectID; stored != "" {
		path, _ := configPath()
		return stored, path
	}
	return "", "the credential"
}

func projectUseCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "use <project>",
		Short: "Save the default target project",
		Long: `Save the default target project.

Accepts an id, a name or a slug. The project is checked against the API before
being saved, and its id is what gets written: a renamed project keeps working,
and later commands do not have to resolve it again.

The choice is written to ~/.config/nemau/config.json. No credential is stored
there; credentials stay in the environment.`,
		Args: requireArgs(1, "nemau project use <project>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			project, err := c.Projects.Get(ctx, args[0])
			if err != nil {
				if nemau.IsNotFound(err) {
					return fmt.Errorf("project %q not found in this organization; "+
						`"nemau project list" shows the available projects`, args[0])
				}
				return err
			}

			stored := loadConfig()
			stored.ProjectID = project.ID
			if err := saveConfig(stored); err != nil {
				return err
			}

			path, _ := configPath()
			fmt.Fprintf(cmd.ErrOrStderr(), "project %s (%s) saved to %s\n",
				project.Name, project.ID, path)
			// A project that is not ready refuses every create: warn now, before
			// the next create fails with a less clear message.
			if !project.Ready() {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"warning: this project is %s and will refuse every create\n", projectState(*project))
			}
			// The environment variable takes precedence over the file, so the
			// saved choice would have no effect on the next command.
			if env := os.Getenv(nemau.EnvProject); env != "" && env != project.ID {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"warning: %s=%s is set and wins over this choice\n", nemau.EnvProject, env)
			}
			return render(cmd, view{
				columns: []string{"ID", "NAME", "REGION", "STATE"},
				rows:    [][]string{{project.ID, project.Name, project.Region, projectState(*project)}},
				data:    project, id: project.ID,
			})
		},
	}
}

func projectUnsetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "unset",
		Short: "Forget the saved project",
		Long: `Forget the saved project.

Later commands fall back to the project the credential carries.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			stored := loadConfig()
			if stored.ProjectID == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "no project was saved")
				return nil
			}
			stored.ProjectID = ""
			if err := saveConfig(stored); err != nil {
				return err
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "saved project forgotten")
			return nil
		},
	}
}

// uuidLike matches the identifier shape the platform generates for a project.
var uuidLike = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// resolveProjectSelection turns a --project or NEMAU_PROJECT_ID value that names
// a project into the identifier the API expects.
//
// An id is passed through unchanged, so the common case costs no extra
// request. A name costs one listing request. That request works even with the
// wrong project on the header, because the listing is scoped to the
// organization and ignores it.
func resolveProjectSelection(ctx context.Context, c *nemau.Client) (*nemau.Client, error) {
	selection := c.Project()
	if selection == "" || uuidLike.MatchString(selection) {
		return c, nil
	}

	project, err := c.Projects.Get(ctx, selection)
	if err != nil {
		if nemau.IsNotFound(err) {
			return nil, fmt.Errorf("project %q not found in this organization; "+
				`"nemau project list" shows the available projects`, selection)
		}
		return nil, fmt.Errorf("resolving project %q: %w", selection, err)
	}
	return c.ForProject(project.ID), nil
}
