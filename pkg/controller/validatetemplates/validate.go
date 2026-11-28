// Package validatetemplates checks the template-1.json files a pull request writes or
// removes against the definitions they are rendered from.
//
// aqua reads a template as it is, and what it trusts is that ar2 rendered it from a
// definition that was reviewed. The catalogue's pull request is how a template arrives, so
// this is what makes sure that what it brings is that rendering and nothing else.
package validatetemplates

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"go.yaml.in/yaml/v4"
)

var (
	errNotRendered = errors.New("the template isn't what ar2 renders from the package's definition")
	errNoTemplate  = errors.New("the package isn't served as a template, so it has no template")
	errIgnored     = errors.New("the package is ignored, so it has no template")
	errLeftBehind  = errors.New("the template was removed while the package is still served as one")
	errNotTemplate = errors.New("not a template-1.json")
)

// Validate checks each template path against the registry.yaml beside it and ar2.yaml,
// both read from the working directory, which is the pull request merged into its base.
//
// A path that exists must be what MarshalTemplate renders from a definition that declares a
// template and isn't ignored. A path that doesn't exist was removed, which is right only for
// a package that isn't, or no longer is, served as a template.
func Validate(w io.Writer, registryConfigPath string, paths []string) error {
	rcfg := &g2.RegistryConfig{}
	if b, err := os.ReadFile(registryConfigPath); err == nil {
		rcfg, err = g2.ParseRegistryConfig(b)
		if err != nil {
			return err //nolint:wrapcheck
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read the registry configuration: %w", err)
	}
	var errs []error
	for _, path := range paths {
		if err := validate(rcfg, path); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		fmt.Fprintf(w, "%s: as rendered\n", path)
	}
	return errors.Join(errs...)
}

func validate(rcfg *g2.RegistryConfig, path string) error {
	if filepath.Base(path) != g2.TemplateFileName {
		return errNotTemplate
	}
	cfg, err := definition(filepath.Join(filepath.Dir(path), g2.ConfigFileName))
	if err != nil {
		return err
	}
	held, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return removed(rcfg, cfg)
	}
	if err != nil {
		return fmt.Errorf("read the template: %w", err)
	}
	return written(rcfg, cfg, held)
}

// removed checks a template a pull request removed.
func removed(rcfg *g2.RegistryConfig, cfg *aquag2.Config) error {
	if cfg != nil && g2.IsTemplate(cfg) && !rcfg.Ignores(cfg.Name) {
		return errLeftBehind
	}
	return nil
}

// written checks a template a pull request wrote.
func written(rcfg *g2.RegistryConfig, cfg *aquag2.Config, held []byte) error {
	if cfg == nil || !g2.IsTemplate(cfg) {
		return errNoTemplate
	}
	if rcfg.Ignores(cfg.Name) {
		return errIgnored
	}
	want, err := g2.MarshalTemplate(cfg)
	if err != nil {
		return err //nolint:wrapcheck
	}
	if !bytes.Equal(want, held) {
		return errNotRendered
	}
	return nil
}

// definition reads a package's registry.yaml, or nil when there is none.
func definition(path string) (*aquag2.Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil //nolint:nilnil // no definition, which the caller decides about
	}
	if err != nil {
		return nil, fmt.Errorf("read the definition: %w", err)
	}
	cfg := &aquag2.Config{}
	if err := yaml.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("read the definition as YAML: %w", err)
	}
	if cfg.PackageInfo == nil {
		return nil, nil //nolint:nilnil
	}
	return cfg, nil
}
