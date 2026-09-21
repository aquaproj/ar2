// Package initcmd implements the 'ar2 init' command.
package initcmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	gogithub "github.com/google/go-github/v91/github"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
	"github.com/szksh-lab-2/ar2/pkg/cli/flag"
	"github.com/szksh-lab-2/ar2/pkg/controller/initcmd"
	"github.com/szksh-lab-2/ar2/pkg/github"
	"github.com/szksh-lab-2/ar2/pkg/state"
	"golang.org/x/oauth2"
)

// Args holds the flag values of the init command.
type Args struct {
	*flag.GlobalFlags

	RegistryRef string
	Registry    string
	Repository  string
	Username    string
	Output      string
}

// New creates the 'ar2 init' command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{
		GlobalFlags: gFlags,
	}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create ar2's initial state and push it to the container registry",
		Long: `Create ar2's initial state and push it to the container registry.

It reads the package list from aqua-registry's registry.yaml, looks up the star count
of each package's GitHub repository, and pushes the result to the container registry.
The star count decides the order in which packages are processed, because the whole
registry can't be generated in one run.

The GitHub access token is read from the GITHUB_TOKEN environment variable.
The container registry repository defaults to the GITHUB_REPOSITORY environment
variable, which GitHub Actions sets.

$ ar2 init
$ ar2 init --output state.json  # write the state to a file instead of pushing it`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return action(cmd.Context(), logger, args)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&args.RegistryRef, "registry-ref", "main", "the aqua-registry ref the package list is read from")
	fs.StringVar(&args.Registry, "registry", "ghcr.io", "the container registry the state is pushed to")
	fs.StringVar(&args.Repository, "repository", "", "the container registry repository (default $GITHUB_REPOSITORY)")
	fs.StringVar(&args.Username, "username", "", "the user the GitHub access token belongs to (default the owner of --repository)")
	fs.StringVar(&args.Output, "output", "", "write the state to this file instead of pushing it")
	return cmd
}

func action(ctx context.Context, logger *slogutil.Logger, args *Args) error {
	if err := logger.SetLevel(args.LogLevel); err != nil {
		return fmt.Errorf("set log level: %w", err)
	}

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return errTokenRequired
	}

	reg, err := containerRegistry(args)
	if err != nil {
		return err
	}

	httpClient := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token}))
	gh, err := gogithub.NewClient(gogithub.WithAuthToken(token))
	if err != nil {
		return fmt.Errorf("create a GitHub client: %w", err)
	}
	ctrl := initcmd.New(gh, github.NewClient(httpClient))
	return ctrl.Init(ctx, logger.Logger, &initcmd.Input{ //nolint:wrapcheck
		RegistryRef: args.RegistryRef,
		GitHubToken: token,
		Registry:    reg,
		Output:      args.Output,
	})
}

// containerRegistry resolves where the state is pushed. It is resolved even when
// --output is given so that a misconfiguration is reported the same way either way.
func containerRegistry(args *Args) (*state.Registry, error) {
	repository := args.Repository
	if repository == "" {
		repository = os.Getenv("GITHUB_REPOSITORY")
	}
	if repository == "" {
		return nil, errRepositoryRequired
	}
	owner, _, found := strings.Cut(repository, "/")
	if !found {
		return nil, errRepositoryFormat
	}
	username := args.Username
	if username == "" {
		username = owner
	}
	return &state.Registry{
		Registry:   args.Registry,
		Repository: repository,
		Username:   username,
	}, nil
}
