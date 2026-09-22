package generate

import (
	"log/slog"
	"testing"

	aquaregistry "github.com/aquaproj/aqua/v2/pkg/config/registry"
)

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// luau-lang/luau calls its Linux build luau-ubuntu.zip. aqua gr can't read "ubuntu",
// so it inferred a package with no Linux at all — not a wrong asset name for Linux,
// no Linux.
func TestRecoverEnvs(t *testing.T) {
	t.Parallel()
	inferred := &aquaregistry.PackageInfo{
		Asset:         "luau-{{.OS}}.{{.Format}}",
		Format:        "zip",
		Replacements:  aquaregistry.Replacements{"darwin": "macos"},
		SupportedEnvs: aquaregistry.SupportedEnvs{"darwin", "windows/amd64"},
	}
	base := &aquaregistry.PackageInfo{
		Replacements:  aquaregistry.Replacements{"darwin": "macos", "linux": "ubuntu"},
		SupportedEnvs: aquaregistry.SupportedEnvs{"darwin", "linux", "windows/amd64"},
	}
	recoverEnvs(discard(), inferred, base, names("luau-macos.zip", "luau-ubuntu.zip", "luau-windows.zip"), "0.739")

	if !supports(inferred, "linux", "amd64") {
		t.Errorf("linux wasn't put back: %v", inferred.SupportedEnvs)
	}
	if got := inferred.Replacements["linux"]; got != "ubuntu" {
		t.Errorf("the spelling is %q, want ubuntu", got)
	}
}

// The definition says which environments exist; the release says what the file is
// called. Without the first, a template with no {{.Arch}} renders the same name for
// every architecture and every one of them looks as though it were there.
func TestRecoverEnvs_theDefinitionDecidesWhatExists(t *testing.T) {
	t.Parallel()
	inferred := &aquaregistry.PackageInfo{
		Asset:         "luau-{{.OS}}.{{.Format}}",
		Format:        "zip",
		SupportedEnvs: aquaregistry.SupportedEnvs{"darwin"},
	}
	base := &aquaregistry.PackageInfo{
		Replacements: aquaregistry.Replacements{"linux": "ubuntu"},
		// windows is not among them, though luau-windows.zip is right there.
		SupportedEnvs: aquaregistry.SupportedEnvs{"darwin", "linux"},
	}
	recoverEnvs(discard(), inferred, base, names("luau-ubuntu.zip", "luau-windows.zip"), "0.739")

	if supports(inferred, "windows", "amd64") {
		t.Errorf("an environment the definition doesn't claim was put back: %v", inferred.SupportedEnvs)
	}
	if !supports(inferred, "linux", "amd64") {
		t.Errorf("linux wasn't put back: %v", inferred.SupportedEnvs)
	}
}

// Nothing is assumed: an environment is put back only when the asset it would
// download is there.
func TestRecoverEnvs_noSuchAsset(t *testing.T) {
	t.Parallel()
	inferred := &aquaregistry.PackageInfo{
		Asset:         "tool-{{.OS}}.tar.gz",
		SupportedEnvs: aquaregistry.SupportedEnvs{"darwin"},
	}
	base := &aquaregistry.PackageInfo{
		Replacements:  aquaregistry.Replacements{"linux": "ubuntu"},
		SupportedEnvs: aquaregistry.SupportedEnvs{"darwin", "linux"},
	}
	recoverEnvs(discard(), inferred, base, names("tool-macos.tar.gz"), "v1.0.0")
	if supports(inferred, "linux", "amd64") {
		t.Errorf("an environment with no asset was put back: %v", inferred.SupportedEnvs)
	}
}
