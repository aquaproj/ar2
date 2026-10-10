package rewrite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
)

var errMismatch = errors.New("the pull request isn't what rewriting the base gives")

// Verify checks that head is base rewritten: the same packages and the same versions, every
// definition and registry.json exactly what rewriting base's gives, and every versions.json
// what its files and its versions directory give.
//
// This is how the rewrite's pull request is checked instead of by downloading every asset
// again. It asserts nothing new about any release, so what has to be true is that it says
// what it replaced said, and that is decided by deriving it again.
func (c *Controller) Verify(ctx context.Context, logger *slog.Logger, base, head string) error {
	before, err := c.load(ctx, logger, base)
	if err != nil {
		return err
	}
	after, err := c.load(ctx, logger, head)
	if err != nil {
		return err
	}
	ids, err := c.repoIDs(ctx, before)
	if err != nil {
		return err
	}
	oids, err := c.versionsTrees(ctx, head, after)
	if err != nil {
		return err
	}

	problems := 0
	report := func(dir, what string) {
		problems++
		logger.Error("differs from rewriting the base", "dir", dir, "file", what)
	}
	for _, dir := range slices.Sorted(maps.Keys(before)) {
		if _, ok := after[dir]; !ok {
			report(dir, "the package is gone")
		}
	}
	for _, dir := range slices.Sorted(maps.Keys(after)) {
		was, ok := before[dir]
		if !ok {
			report(dir, "a package the base doesn't hold")
			continue
		}
		verifyPackage(logger, was, after[dir], idOf(was, ids), oids[dir], report)
	}
	logger.Info("compared every package with rewriting the base", "num_of_packages", len(after), "num_of_problems", problems)
	if problems > 0 {
		return fmt.Errorf("%w: %d problems", errMismatch, problems)
	}
	return nil
}

// verifyPackage compares one package with rewriting it, and reports what differs.
func verifyPackage(logger *slog.Logger, was, now *pkg, id int64, source string, report func(dir, what string)) {
	def, files, err := rewritten(was, id)
	if err != nil {
		report(was.dir, err.Error())
		return
	}
	if now.definition != def {
		report(was.dir, g2.ConfigFileName)
	}
	compareFiles(was.dir, files, now.files, report)
	if len(now.files) == 0 {
		return
	}
	list, err := g2.RenderVersionsList(logger, source, now.files)
	if err != nil {
		report(was.dir, err.Error())
		return
	}
	if now.list != list {
		report(was.dir, aquag2.VersionsFileName)
	}
}

// compareFiles reports each version whose file isn't the rewritten one, and each version
// only one side holds.
func compareFiles(dir string, want, got map[string]string, report func(dir, what string)) {
	for _, version := range slices.Sorted(maps.Keys(want)) {
		g, ok := got[version]
		switch {
		case !ok:
			report(dir, version+": gone")
		case g != want[version]:
			report(dir, version)
		}
	}
	for version := range got {
		if _, ok := want[version]; !ok {
			report(dir, version+": not in the base")
		}
	}
}

// versionsTrees is the sha of each package's versions directory on a ref, by package.
func (c *Controller) versionsTrees(ctx context.Context, ref string, pkgs map[string]*pkg) (map[string]string, error) {
	expr := func(dir string) string { return ref + ":" + dir + "/" + aquag2.VersionDir }
	exprs := []string{}
	for dir, p := range pkgs {
		if len(p.files) > 0 {
			exprs = append(exprs, expr(dir))
		}
	}
	oids, err := c.repo.OIDs(ctx, exprs)
	if err != nil {
		return nil, fmt.Errorf("read the versions directories: %w", err)
	}
	out := make(map[string]string, len(oids))
	for dir := range pkgs {
		if oid, ok := oids[expr(dir)]; ok {
			out[dir] = oid
		}
	}
	return out, nil
}
