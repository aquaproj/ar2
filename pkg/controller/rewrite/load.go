package rewrite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"go.yaml.in/yaml/v3"
)

// load reads every package a ref holds.
func (c *Controller) load(ctx context.Context, logger *slog.Logger, ref string) (map[string]*pkg, error) {
	dirs, err := c.repo.Subtrees(ctx, ref+":"+g2.PackagesDir)
	if err != nil {
		return nil, fmt.Errorf("list the packages: %w", err)
	}
	pkgs := make(map[string]*pkg, len(dirs))
	for _, d := range dirs {
		dir := g2.PackagesDir + "/" + d
		pkgs[dir] = &pkg{dir: dir, files: map[string]string{}}
	}
	if err := c.readDefinitions(ctx, logger, ref, pkgs); err != nil {
		return nil, err
	}
	if err := c.readFiles(ctx, logger, ref, pkgs); err != nil {
		return nil, err
	}
	return pkgs, nil
}

// readDefinitions reads each package's definition and its list of versions.
func (c *Controller) readDefinitions(ctx context.Context, logger *slog.Logger, ref string, pkgs map[string]*pkg) error {
	defExpr := func(dir string) string { return ref + ":" + dir + "/" + g2.ConfigFileName }
	listExpr := func(dir string) string { return ref + ":" + dir + "/" + aquag2.VersionsFileName }
	exprs := make([]string, 0, len(pkgs)*2) //nolint:mnd // two files a package
	for dir := range pkgs {
		exprs = append(exprs, defExpr(dir), listExpr(dir))
	}
	blobs, err := c.repo.Blobs(ctx, logger, exprs)
	if err != nil {
		return fmt.Errorf("read the definitions: %w", err)
	}
	for dir, p := range pkgs {
		p.definition = blobs[defExpr(dir)]
		p.list = blobs[listExpr(dir)]
		p.config = &aquag2.Config{}
		if err := yaml.Unmarshal([]byte(p.definition), p.config); err != nil {
			return fmt.Errorf("read the definition of %s: %w", dir, err)
		}
	}
	return nil
}

// versionFile is one version's registry.json, located.
type versionFile struct {
	pkg     *pkg
	version string
}

// readFiles reads every version's registry.json.
func (c *Controller) readFiles(ctx context.Context, logger *slog.Logger, ref string, pkgs map[string]*pkg) error {
	treeExpr := func(dir string) string { return ref + ":" + dir + "/" + aquag2.VersionDir }
	treeExprs := make([]string, 0, len(pkgs))
	for dir := range pkgs {
		treeExprs = append(treeExprs, treeExpr(dir))
	}
	trees, err := c.repo.FilesTwoDeep(ctx, treeExprs)
	if err != nil {
		return fmt.Errorf("list the versions: %w", err)
	}
	located := map[string]versionFile{}
	exprs := []string{}
	for dir, p := range pkgs {
		for _, f := range trees[treeExpr(dir)] {
			version, ok := versionOfPath(f)
			if !ok {
				continue
			}
			expr := treeExpr(dir) + "/" + f
			exprs = append(exprs, expr)
			located[expr] = versionFile{pkg: p, version: version}
		}
	}
	contents, err := c.repo.Blobs(ctx, logger, exprs)
	if err != nil {
		return fmt.Errorf("read the versions: %w", err)
	}
	for _, expr := range exprs {
		content, ok := contents[expr]
		if !ok {
			return fmt.Errorf("%w: %s", errUnreadable, expr)
		}
		located[expr].pkg.files[located[expr].version] = content
	}
	return nil
}

// versionOfPath is the version whose registry.json a path under versions/ is.
func versionOfPath(f string) (string, bool) {
	dirName, file := path.Split(f)
	if file != aquag2.FileName {
		return "", false
	}
	return aquag2.DecodeVersion(strings.TrimSuffix(dirName, "/"))
}

var errUnreadable = errors.New("a file couldn't be read whole")
