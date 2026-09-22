package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/aqua/v2/pkg/expr"
	"github.com/aquaproj/ar2/pkg/migrate"
	"github.com/expr-lang/expr/vm"
	gogithub "github.com/google/go-github/v92/github"
)

// releasesRead is how far back the comparison goes. A package that resolved the same
// way for its last hundred releases is not going to be told apart by its hundred and
// first.
const releasesRead = 100

// realTags compares the two definitions on the versions a package really has.
//
// Synthesising versions is enough to find which packages to look at and not enough to
// say what is wrong with them, because a synthetic version a package never had is
// compared against overrides that were never meant to match it. These are the tags
// the package published, filtered exactly as ar2 filters them before generating, so a
// difference here is a difference a user would get.
//
// It reads the GitHub API, so it takes a list of packages rather than a whole
// registry.
func realTags(w io.Writer, root string, packages []string) error {
	wanted := map[string]struct{}{}
	for _, p := range packages {
		wanted[p] = struct{}{}
	}
	gh, err := gogithub.NewClient(gogithub.WithAuthToken(os.Getenv("GITHUB_TOKEN")))
	if err != nil {
		return fmt.Errorf("create a GitHub client: %w", err)
	}
	ctx := context.Background()
	differ := 0

	if err := walkPackages(root, func(src *aquaregistry.PackageInfo) {
		if _, ok := wanted[src.GetName()]; !ok {
			return
		}
		if comparePublished(ctx, w, gh, src) {
			differ++
		}
	}); err != nil {
		return err
	}

	fmt.Fprintf(w, "\npackages that resolve differently on real tags: %d / %d\n", differ, len(wanted))
	return nil
}

// comparePublished compares the two definitions of one package on the versions it
// published, and reports whether any of them resolved differently.
func comparePublished(ctx context.Context, w io.Writer, gh *gogithub.Client, src *aquaregistry.PackageInfo) bool {
	name := src.GetName()
	tags, err := publishedTags(ctx, gh, src)
	if err != nil {
		fmt.Fprintf(w, "%-40s %v\n", name, err)
		return false
	}
	logger := slog.New(slog.DiscardHandler)
	cfg, _ := migrate.Config(src.Copy(), nil)
	var bad []string
	compared, shown := 0, false
	for _, v := range tags {
		a, errA := src.Copy().SetVersion(logger, v)
		b, errB := cfg.SetVersion(logger, v)
		if errA != nil || errB != nil {
			continue
		}
		compared++
		lines := differingFields(comparedFields(a), comparedFields(b))
		if len(lines) == 0 {
			continue
		}
		bad = append(bad, v)
		if !shown {
			shown = true
			fmt.Fprintf(w, "--- %s @ %s\n", name, v)
			for _, line := range lines {
				fmt.Fprintln(w, "   ", line)
			}
		}
	}
	fmt.Fprintf(w, "%-40s compared %d tags\n", name, compared)
	if len(bad) == 0 {
		return false
	}
	fmt.Fprintf(w, "%-40s %v\n", name, bad)
	return true
}

// publishedTags returns the versions of the package that generation would see: its
// releases, without drafts and prereleases, through its version_filter.
//
// The filter matters for a monorepo, which tags every component it holds. A tag that
// isn't a version of this package never reaches generation, so a difference on one
// says nothing.
func publishedTags(ctx context.Context, gh *gogithub.Client, src *aquaregistry.PackageInfo) ([]string, error) {
	var filter *vm.Program
	if src.VersionFilter != "" {
		f, err := expr.CompileVersionFilter(src.VersionFilter)
		if err != nil {
			return nil, fmt.Errorf("compile the version filter: %w", err)
		}
		filter = f
	}
	releases, _, err := gh.Repositories.ListReleases(ctx, src.RepoOwner, src.RepoName, &gogithub.ListOptions{PerPage: releasesRead})
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	logger := slog.New(slog.DiscardHandler)
	tags := make([]string, 0, len(releases))
	for _, r := range releases {
		if r.GetDraft() || r.GetPrerelease() {
			continue
		}
		v := r.GetTagName()
		if filter != nil {
			ok, err := expr.EvaluateVersionFilter(logger, filter, v)
			if err != nil || !ok {
				continue
			}
		}
		tags = append(tags, v)
	}
	return tags, nil
}

// differingFields reports the fields the two resolutions disagree on.
func differingFields(x, y map[string]string) []string {
	var lines []string
	for _, name := range comparedFieldNames() {
		if x[name] == y[name] {
			continue
		}
		lines = append(lines, fmt.Sprintf("%-14s v1=%s", name, oneLine(x[name], diffWidth)))
		lines = append(lines, fmt.Sprintf("%-14s g2=%s", "", oneLine(y[name], diffWidth)))
	}
	return lines
}
