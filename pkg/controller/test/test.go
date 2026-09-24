// Package test checks a generated registry.json against the release it describes.
//
// registry.json is what aqua installs from, and a pull request carrying one is merged
// without a human reading it, so this is where the trust in the file comes from. The
// generator established the same things when it wrote the file; checking them again
// here is what makes the merge safe rather than a matter of believing the generator.
//
// What can be checked depends on the machine. An asset in a format this machine can't
// open is reported and passed over rather than failed, which is why this runs on a
// matrix: between the runners, every format a registry holds is opened somewhere.
package test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/aquaproj/ar2/pkg/sign"
	"github.com/aquaproj/ar2/pkg/verify"
	"github.com/suzuki-shunsuke/slog-error/slogerr"
)

// errFailed says that at least one check failed. What failed has been logged as it
// happened, because a run over a matrix of runners is read as a log rather than as a
// return value.
var errFailed = errors.New("the registry didn't hold up to its release")

// Verifier downloads an asset and establishes what it actually holds.
type Verifier interface {
	Verify(ctx context.Context, logger *slog.Logger, pkgName, version string, asset *generate.Asset) (*verify.Result, error)
}

// Environment is one os and arch, as an entry names them.
type Environment struct {
	OS   string
	Arch string
}

// String renders the environment the way a registry writes it.
func (e Environment) String() string {
	return e.OS + "/" + e.Arch
}

// Empty says the caller named no environment, so every entry is checked.
func (e Environment) Empty() bool {
	return e.OS == "" && e.Arch == ""
}

// matches reports whether the entry belongs to the environment.
func (e Environment) matches(asset *generate.Asset) bool {
	return e.Empty() || (asset.OS == e.OS && asset.Arch == e.Arch)
}

// Controller checks registry.json files.
type Controller struct {
	verifier Verifier
	// env limits the run to the entries of one environment, which is how a job
	// running on a machine of that environment checks the entry meant for it: an
	// archive is opened by its format rather than by its platform, but running what
	// is inside it isn't, and checking an entry where it will be installed is what
	// makes that possible.
	env Environment
}

// New creates a Controller. An empty environment checks every entry.
func New(verifier Verifier, env Environment) *Controller {
	return &Controller{verifier: verifier, env: env}
}

// Run checks every file and fails if any of them didn't hold up.
//
// Every file is checked before returning rather than stopping at the first failure: a
// pull request adding several versions should say what is wrong with all of them.
func (c *Controller) Run(ctx context.Context, logger *slog.Logger, pkgName string, paths []string) error {
	failed := false
	for _, path := range paths {
		logger := logger.With("file", path)
		if err := c.check(ctx, logger, pkgName, path); err != nil {
			slogerr.WithError(logger, err).Error("check the registry")
			failed = true
		}
	}
	if failed {
		return errFailed
	}
	return nil
}

// check reads one registry.json and checks every asset in it.
func (c *Controller) check(ctx context.Context, logger *slog.Logger, pkgName, path string) error {
	reg, err := read(path)
	if err != nil {
		return err
	}
	version := versionFromPath(path)
	if version == "" {
		return errNoVersionInPath
	}
	logger = logger.With("package_version", version)
	if len(reg.Assets) == 0 {
		return errNoAsset
	}

	failed := false
	checked := 0
	for _, asset := range reg.Assets {
		if !c.env.matches(asset) {
			continue
		}
		checked++
		logger := logger.With("asset_env", asset.OS+"/"+asset.Arch)
		if err := shape(asset); err != nil {
			slogerr.WithError(logger, err).Error("the entry isn't complete")
			failed = true
			continue
		}
		if err := c.asset(ctx, logger, pkgName, version, asset); err != nil {
			slogerr.WithError(logger, err).Error("the entry doesn't describe the release")
			failed = true
		}
	}
	if failed {
		return errFailed
	}
	if checked == 0 {
		// The file describes no such environment. A job runs per environment the
		// files hold, so this is a file that doesn't hold the one it was given, not
		// a package that fails on it.
		logger.Info("the registry has nothing for this environment", "environment", c.env)
		return nil
	}
	logger.Info("the registry holds up to its release", "assets", checked)
	return nil
}

// Environments lists the environments the files describe, sorted, without repeats.
//
// The checks run one job per environment, on a machine of that environment, and the
// jobs are worked out from this: a fixed list would leave an entry for an environment
// nobody thought of unchecked, which is the one thing a merge gate must not do.
func Environments(w io.Writer, paths []string) error {
	seen := map[string]struct{}{}
	for _, path := range paths {
		reg, err := read(path)
		if err != nil {
			return err
		}
		for _, asset := range reg.Assets {
			if asset.OS == "" || asset.Arch == "" {
				return errNoEnv
			}
			seen[Environment{OS: asset.OS, Arch: asset.Arch}.String()] = struct{}{}
		}
	}
	envs := make([]string, 0, len(seen))
	for env := range seen {
		envs = append(envs, env)
	}
	sort.Strings(envs)
	for _, env := range envs {
		fmt.Fprintln(w, env)
	}
	return nil
}

// asset downloads one asset and compares what it holds with what the entry says.
func (c *Controller) asset(ctx context.Context, logger *slog.Logger, pkgName, version string, asset *generate.Asset) error {
	if !downloadable(asset.Type) {
		// go_install and cargo build through another tool, which resolves and
		// verifies for itself. There is no asset here to download.
		logger.Debug("nothing to download for this type", "type", asset.Type)
		return nil
	}
	if !verify.Extractable(asset.Format) {
		logger.Info("this machine can't open the format, so the entry goes unchecked here",
			"format", asset.Format)
		return nil
	}

	result, err := c.verifier.Verify(ctx, logger, pkgName, version, asset)
	if err != nil {
		// A signature that didn't hold says what it was; anything else got as far as
		// the archive or not at all.
		if errors.Is(err, sign.ErrUnverified) {
			return err //nolint:wrapcheck // it already says which signature and why
		}
		return fmt.Errorf("extract the asset: %w", err)
	}
	if !strings.EqualFold(result.Checksum, asset.Checksum) {
		return fmt.Errorf("%w: the entry says %s and the asset hashes to %s",
			errChecksum, asset.Checksum, result.Checksum)
	}
	if len(result.Unresolved) > 0 {
		return fmt.Errorf("%w: %s", errFilesMissing, strings.Join(result.Unresolved, ", "))
	}
	if result.NeedsReview {
		// The archive holds the files under paths other than the ones recorded, which
		// the generator would have relocated. An entry that has already been written
		// is simply wrong.
		return errFilesMoved
	}
	return nil
}

// read parses the file, rejecting a field aqua wouldn't read.
//
// An unknown field is how a registry.json written by something newer, or by something
// mistaken, arrives. Either way aqua would ignore it, so the entry would not mean what
// the file says.
func read(path string) (*generate.Registry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open the registry: %w", err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	reg := &generate.Registry{}
	if err := dec.Decode(reg); err != nil {
		return nil, fmt.Errorf("read the registry as JSON: %w", err)
	}
	// A file holding a second document is a file somebody wrote by hand.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errTrailingContent
	}
	return reg, nil
}

// shape checks what an entry has to say whatever the release turns out to hold.
func shape(asset *generate.Asset) error {
	if asset.OS == "" || asset.Arch == "" {
		return errNoEnv
	}
	if asset.Type == "" {
		return errNoType
	}
	if downloadable(asset.Type) && asset.Checksum == "" {
		// Without one there is nothing for an install to verify, which is the whole
		// point of generating these files.
		return errNoChecksum
	}
	return nil
}

// downloadable reports whether the type installs by downloading an asset. The others
// build from source through a tool that verifies what it fetches.
func downloadable(typ string) bool {
	switch typ {
	case "go_install", "cargo":
		return false
	default:
		return true
	}
}

// versionFromPath reads the version out of versions/<version>/registry-1.json, which is
// where a package branch keeps it. The file doesn't hold the version: it is the
// directory's name, so that one version is one directory and a commit touches nothing
// else.
func versionFromPath(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(filepath.Dir(dir)) != "versions" {
		return ""
	}
	return filepath.Base(dir)
}
