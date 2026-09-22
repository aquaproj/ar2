package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/aquaproj/ar2/pkg/migrate"
	"go.yaml.in/yaml/v3"
)

// one shows both resolutions of a single package at a single version.
//
// This is what the other two commands hand their findings to: they say which package
// and which version, and this says what the difference actually is.
func one(w io.Writer, path, version string) error {
	b, err := os.ReadFile(path) //nolint:gosec // a registry.yaml the developer running this points at
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var cfg aquaregistry.Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if len(cfg.PackageInfos) == 0 {
		return fmt.Errorf("%s holds no package", path)
	}
	src := cfg.PackageInfos[0]
	logger := slog.New(slog.DiscardHandler)
	converted, unconverted := migrate.Config(src.Copy(), nil)

	v1, err := src.Copy().SetVersion(logger, version)
	if err != nil {
		return fmt.Errorf("apply the version to the v1 definition: %w", err)
	}
	g2, err := converted.SetVersion(logger, version)
	if err != nil {
		return fmt.Errorf("apply the version to the g2 definition: %w", err)
	}

	if len(unconverted) > 0 {
		fmt.Fprintln(w, "unconverted:", strings.Join(unconverted, " | "))
	}
	fmt.Fprintln(w, "=== v1 ===")
	fmt.Fprintln(w, show(v1))
	fmt.Fprintln(w, "=== g2 ===")
	fmt.Fprintln(w, show(g2))
	return nil
}

func show(p *aquaregistry.PackageInfo) string {
	fields := comparedFields(p)
	var b strings.Builder
	for _, name := range comparedFieldNames() {
		if fields[name] == "null" {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", name, fields[name])
	}
	return strings.TrimRight(b.String(), "\n")
}
