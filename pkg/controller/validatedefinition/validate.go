// Package validatedefinition checks that a package's definition says what the registry
// reads it for.
//
// registry.yaml is the one file in the registry a person writes, and the only thing that
// says which package a branch holds: a branch is named after an id. Nothing on the way in
// read it, so a definition that stopped being YAML passed the branch's checks, merged, and
// left the package generating nothing -- a run says so and moves on, which is a line in a
// log nobody is watching.
package validatedefinition

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aquaproj/aqua/v2/pkg/expr"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
	"go.yaml.in/yaml/v3"
)

// Validate reads the definition at path and reports what is wrong with it.
//
// What it asks is what the registry's own readers ask: that it is YAML, that it names a
// package, and that it says where the package comes from. A branch that holds nothing but
// its claim to a package is right to say no more -- that is what a branch created and not
// yet taken over holds -- so a claim passes.
func Validate(w io.Writer, path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read the definition: %w", err)
	}
	cfg := &aquag2.Config{}
	// Strict, because a field aqua wouldn't read means the file was written by
	// something that disagrees with aqua about what a definition is -- and a
	// misplaced one is how a definition stops saying what it looks like it says.
	dec := yaml.NewDecoder(bytes.NewReader(content))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return fmt.Errorf("read the definition as YAML: %w", err)
	}
	if cfg.PackageInfo == nil {
		return errEmpty
	}
	if cfg.Name == "" {
		return errNoName
	}
	if g2.IsClaim(cfg) {
		// A branch's claim to a package, which is what it holds until a run takes the
		// package over. It says the name and is right to say no more.
		fmt.Fprintf(w, "%s: the branch's claim to the package, and nothing generated from it yet\n", cfg.Name)
		return nil
	}
	if cfg.Type == "" {
		return fmt.Errorf("%w: %s", errNoType, cfg.Name)
	}
	if err := validateAssetRules(cfg.AssetRules); err != nil {
		return fmt.Errorf("%s: %w", cfg.Name, err)
	}
	fmt.Fprintf(w, "%s: %s\n", cfg.Name, cfg.Type)
	return nil
}

// validateAssetRules checks that each asset rule says which environments it is for, as
// os/arch, and has a filter that compiles. A run reads them only when it generates a
// version, which is too late to find a filter that isn't an expression.
func validateAssetRules(rules []*aquag2.AssetRule) error {
	for i, rule := range rules {
		if len(rule.Envs) == 0 {
			return fmt.Errorf("%w: asset_rules[%d]", errAssetRuleNoEnvs, i)
		}
		for _, env := range rule.Envs {
			goos, goarch, ok := strings.Cut(env, "/")
			if !ok || goos == "" || goarch == "" || strings.Contains(goarch, "/") {
				return fmt.Errorf("%w: asset_rules[%d]: %q", errAssetRuleEnv, i, env)
			}
		}
		if rule.Filter == "" {
			return fmt.Errorf("%w: asset_rules[%d]", errAssetRuleNoFilter, i)
		}
		if _, err := expr.CompileAssetFilter(rule.Filter); err != nil {
			return fmt.Errorf("compile asset_rules[%d]'s filter: %w", i, err)
		}
	}
	return nil
}
