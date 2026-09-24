package test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aquaproj/ar2/pkg/generate"
)

func TestVersionFromPath(t *testing.T) {
	t.Parallel()
	data := []struct {
		name string
		path string
		exp  string
	}{
		{
			name: "a package branch's layout",
			path: "versions/v1.5.6/registry-1.json",
			exp:  "v1.5.6",
		},
		{
			name: "checked out somewhere else",
			path: "/tmp/pkg_foo/versions/0.10.20/registry-1.json",
			exp:  "0.10.20",
		},
		{
			// A version whose name looks like a path of its own still comes back
			// whole, because it is one directory on the branch.
			name: "a version with a slash in it",
			path: filepath.Join("versions", "cli-v1.0.0", "registry-1.json"),
			exp:  "cli-v1.0.0",
		},
		{
			name: "not under versions",
			path: "registry.json",
		},
		{
			name: "a directory deeper than the layout",
			path: "versions/v1.5.6/extra/registry-1.json",
		},
	}
	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			if got := versionFromPath(d.path); got != d.exp {
				t.Fatalf("versionFromPath(%q) = %q, wanted %q", d.path, got, d.exp)
			}
		})
	}
}

func TestShape(t *testing.T) {
	t.Parallel()
	data := []struct {
		name    string
		asset   *generate.Asset
		wantErr error
	}{
		{
			name:  "complete",
			asset: &generate.Asset{OS: "linux", Arch: "amd64", Type: "github_release", Checksum: "abc"},
		},
		{
			// The tool that builds it resolves and verifies what it fetches, so there
			// is nothing here to hash.
			name:  "go_install carries no checksum",
			asset: &generate.Asset{OS: "linux", Arch: "amd64", Type: "go_install", Path: "example.com/cmd/foo"},
		},
		{
			name:    "no environment",
			asset:   &generate.Asset{Type: "github_release", Checksum: "abc"},
			wantErr: errNoEnv,
		},
		{
			name:    "no type",
			asset:   &generate.Asset{OS: "linux", Arch: "amd64", Checksum: "abc"},
			wantErr: errNoType,
		},
		{
			name:    "a download with nothing to verify it against",
			asset:   &generate.Asset{OS: "linux", Arch: "amd64", Type: "github_release"},
			wantErr: errNoChecksum,
		},
	}
	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			if got := shape(d.asset); !errors.Is(got, d.wantErr) {
				t.Fatalf("shape() = %v, wanted %v", got, d.wantErr)
			}
		})
	}
}

func TestRead(t *testing.T) {
	t.Parallel()
	data := []struct {
		name    string
		content string
		assets  int
		wantErr bool
	}{
		{
			name:    "a registry",
			content: `{"assets":[{"os":"linux","arch":"amd64","type":"github_release"}]}`,
			assets:  1,
		},
		{
			// aqua would ignore the field, so the entry wouldn't mean what the file
			// says it does.
			name:    "a field aqua doesn't read",
			content: `{"assets":[{"os":"linux","arch":"amd64","type":"github_release","asset_name":"foo"}]}`,
			wantErr: true,
		},
		{
			name:    "not JSON",
			content: "assets:\n  - os: linux\n",
			wantErr: true,
		},
		{
			name:    "two documents",
			content: `{"assets":[]}{"assets":[]}`,
			wantErr: true,
		},
	}
	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "registry.json")
			if err := os.WriteFile(path, []byte(d.content), 0o600); err != nil {
				t.Fatal(err)
			}
			reg, err := read(path)
			if d.wantErr {
				if err == nil {
					t.Fatal("read() wanted an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("read(): %v", err)
			}
			if len(reg.Assets) != d.assets {
				t.Fatalf("read() returned %d assets, wanted %d", len(reg.Assets), d.assets)
			}
		})
	}
}

func TestEnvironment_matches(t *testing.T) {
	t.Parallel()
	linuxAmd64 := &generate.Asset{OS: "linux", Arch: "amd64"}
	data := []struct {
		name string
		env  Environment
		exp  bool
	}{
		{
			// Nobody named an environment, so every entry is checked.
			name: "empty",
			env:  Environment{},
			exp:  true,
		},
		{
			name: "the entry's own",
			env:  Environment{OS: "linux", Arch: "amd64"},
			exp:  true,
		},
		{
			name: "another architecture",
			env:  Environment{OS: "linux", Arch: "arm64"},
		},
		{
			name: "another operating system",
			env:  Environment{OS: "windows", Arch: "amd64"},
		},
	}
	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			if got := d.env.matches(linuxAmd64); got != d.exp {
				t.Fatalf("matches() = %v, wanted %v", got, d.exp)
			}
		})
	}
}

// The jobs are worked out from the files, so an entry for an environment nobody
// thought of gets a job rather than going unchecked.
func TestEnvironments(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	a := write("versions/v1.0.0/registry-1.json", `{"assets":[
		{"os":"linux","arch":"amd64","type":"github_release","checksum":"a"},
		{"os":"darwin","arch":"arm64","type":"github_release","checksum":"b"}]}`)
	b := write("versions/v1.1.0/registry-1.json", `{"assets":[
		{"os":"linux","arch":"amd64","type":"github_release","checksum":"c"},
		{"os":"windows","arch":"arm64","type":"github_release","checksum":"d"}]}`)

	out := &strings.Builder{}
	if err := Environments(out, []string{a, b}); err != nil {
		t.Fatal(err)
	}
	// Sorted, and each environment once however many versions hold it.
	want := "darwin/arm64\nlinux/amd64\nwindows/arm64\n"
	if out.String() != want {
		t.Fatalf("Environments() printed:\n%s\nwanted:\n%s", out.String(), want)
	}
}
