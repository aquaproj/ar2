// Package closeissues is the close-issues command.
package closeissues

import (
	"context"
	"errors"
	"fmt"

	"github.com/aquaproj/ar2/pkg/cli/flag"
	"github.com/aquaproj/ar2/pkg/cli/identities"
	"github.com/aquaproj/ar2/pkg/cli/token"
	ctrl "github.com/aquaproj/ar2/pkg/controller/closeissues"
	"github.com/aquaproj/ar2/pkg/g2"
	gogithub "github.com/google/go-github/v92/github"
	"github.com/spf13/cobra"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
	"golang.org/x/oauth2"
)

var errTokenRequired = errors.New("a GitHub access token is required: set GITHUB_TOKEN or set up ghtkn")

// Args is what the command takes.
type Args struct {
	*flag.GlobalFlags
	DryRun  bool
	G2Owner string
	G2Repo  string
}

// New returns the close-issues command.
func New(logger *slogutil.Logger, gFlags *flag.GlobalFlags) *cobra.Command {
	args := &Args{GlobalFlags: gFlags}
	cmd := &cobra.Command{
		Use:   "close-issues",
		Short: "Close the issues ar2 opened once what they are about is resolved",
		Long: `Close the issues ar2 opened once what they are about is resolved.

A run opens an issue per package for the environments a version was published without
(labelled missing-environment) and for the versions a check of the release's integrity
stopped (labelled integrity), and comments on it again rather than opening another. Each
time it writes what it is about into the issue, as a record the rendered issue doesn't show.

This reads those records and asks the registry about each:

- an environment left out is resolved once the version's registry.json covers it again --
  ar2 regenerate puts it back. A version that isn't served -- its pull request hasn't merged,
  or it was taken out -- resolves nothing.
- a version a check of integrity stopped is resolved once it is served

An issue whose every record is resolved is commented on, saying what was, and closed. One
with anything unresolved stays open, and so does one holding no record, which is one written
before the records were.

$ ar2 close-issues --dry-run

The GitHub access token is read from the GITHUB_TOKEN environment variable, or from ghtkn
when it isn't set. It comments on and closes the issues.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return action(cmd.Context(), logger, args)
		},
	}
	fs := cmd.Flags()
	fs.BoolVar(&args.DryRun, "dry-run", false, "say which issues would be closed without closing any")
	fs.StringVar(&args.G2Owner, "g2-owner", "aquaproj", "the owner of the aqua-registry-g2 repository")
	fs.StringVar(&args.G2Repo, "g2-repo", "aqua-registry-g2", "the aqua-registry-g2 repository")
	return cmd
}

func action(ctx context.Context, logger *slogutil.Logger, args *Args) error {
	if err := logger.SetLevel(args.LogLevel); err != nil {
		return fmt.Errorf("set log level: %w", err)
	}
	ts, err := token.API(logger.Logger)
	if err != nil {
		return err //nolint:wrapcheck // the error already says what failed
	}
	if ts == nil {
		return errTokenRequired
	}
	httpClient := oauth2.NewClient(ctx, ts)
	gh, err := gogithub.NewClient(gogithub.WithHTTPClient(httpClient))
	if err != nil {
		return fmt.Errorf("create a GitHub client: %w", err)
	}
	registry := g2.New(gh, nil, args.G2Owner, args.G2Repo, args.Version)
	// The packages are named in the issues, and their directories are what the identities say.
	if _, _, err := identities.Read(ctx, logger.Logger, registry, httpClient, args.G2Owner, args.G2Repo); err != nil {
		return err //nolint:wrapcheck
	}
	c := ctrl.New(gh.Issues, registry, args.G2Owner, args.G2Repo)
	if args.DryRun {
		c.DryRun()
	}
	return c.Close(ctx, logger.Logger) //nolint:wrapcheck
}
