package sign

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/aquaproj/aqua/v2/pkg/checksum"
	"github.com/aquaproj/aqua/v2/pkg/config"
	"github.com/aquaproj/aqua/v2/pkg/installpackage"
	"github.com/aquaproj/aqua/v2/pkg/runtime"
)

// The programs a verification is run with, as the verifier knows them.
const (
	toolCosign   = "cosign"
	toolSLSA     = "slsa"
	toolMinisign = "minisign"
	toolGH       = "gh"
)

// tool is one of the programs a verification is run with.
//
// aqua's verifiers only work out where their tool is; putting it there is the install
// path's job, which aqua does with a dedicated installer per tool before it verifies
// anything. ar2 uses the verifiers without that path, so it has to do the same. Until
// it did, cosign was never installed and every signature it should have checked came
// back as one that couldn't be checked -- indistinguishable, from the outside, from a
// release that had stopped being signed.
type tool struct {
	pkg       func() *config.Package
	checksums *checksum.Checksums
	once      sync.Once
	err       error
}

// ensure installs the tool the first time it is asked for, and reports the same
// answer every time after. A run verifies thousands of assets and the tool is the
// same one throughout.
func (t *tool) ensure(ctx context.Context, logger *slog.Logger, inst *installpackage.Installer, rt *runtime.Runtime) error {
	t.once.Do(func() {
		t.err = t.install(ctx, logger, inst, rt)
	})
	return t.err
}

func (t *tool) install(ctx context.Context, logger *slog.Logger, inst *installpackage.Installer, rt *runtime.Runtime) error {
	pkg := t.pkg()
	logger = logger.With("tool", pkg.Package.Name, "tool_version", pkg.Package.Version)

	pkgInfo, err := pkg.PackageInfo.Override(logger, pkg.Package.Version, rt)
	if err != nil {
		return fmt.Errorf("evaluate the version constraints of the tool: %w", err)
	}
	supported, err := pkgInfo.CheckSupported(rt, rt.Env())
	if err != nil {
		return fmt.Errorf("check whether the tool supports this environment: %w", err)
	}
	if !supported {
		// Nothing to install. The verifier refuses for itself on an environment its
		// tool doesn't cover, and says so in its own words.
		logger.Debug("the tool isn't supported in this environment")
		return nil
	}
	pkg.PackageInfo = pkgInfo

	// The checksums are the ones aqua ships for the version it pins, so the download
	// is checked against what aqua expects rather than against the registry this run
	// is building. No policy either: the tool isn't a package anyone asked for.
	if err := inst.InstallPackage(ctx, logger, &installpackage.ParamInstallPackage{
		Pkg:       pkg,
		Checksums: t.checksums,

		DisablePolicy: true,
	}); err != nil {
		return fmt.Errorf("install the tool: %w", err)
	}
	return nil
}
