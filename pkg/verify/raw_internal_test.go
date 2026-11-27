package verify

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aquaproj/ar2/pkg/generate"
	"github.com/google/go-cmp/cmp"
)

// An asset that isn't an archive is the command, so one that can't be run is the wrong
// asset: kubernetes-sigs/zeitgeist's windows/amd64 entry installed zeitgeist.intoto.json.
func TestUnrunnableRaw(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"zeitgeist.intoto.json":       `{"payloadType":"application/vnd.in-toto+json"}`,
		"zeitgeist-amd64-linux":       "\x7fELF\x02\x01",
		"zeitgeist-amd64-windows.exe": "MZ\x90\x00",
		"tool.jar":                    "PK\x03\x04",
		"sass.bat":                    "@echo off",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name  string
		asset *generate.Asset
		want  []string
	}{
		{
			name:  "a provenance taken for the binary",
			asset: &generate.Asset{OS: "windows", Format: "raw", Asset: "zeitgeist.intoto.json"},
			want:  []string{"zeitgeist.intoto.json"},
		},
		{
			name:  "a binary",
			asset: &generate.Asset{OS: "linux", Format: "raw", Asset: "zeitgeist-amd64-linux"},
		},
		{
			name:  "a Windows executable",
			asset: &generate.Asset{OS: "windows", Format: "raw", Asset: "zeitgeist-amd64-windows.exe"},
		},
		{
			name:  "a jar, which java runs",
			asset: &generate.Asset{OS: "linux", Format: "raw", Asset: "tool.jar"},
		},
		{
			name:  "a script Windows runs by its extension",
			asset: &generate.Asset{OS: "windows", Format: "raw", Asset: "sass.bat"},
		},
		{
			name:  "an archive, whose files resolveFiles already found runnable",
			asset: &generate.Asset{OS: "windows", Format: "zip", Asset: "zeitgeist.zip"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			files := []*generate.File{{Name: "zeitgeist", Src: tt.asset.Asset}}
			if diff := cmp.Diff(tt.want, unrunnableRaw(dir, tt.asset, files)); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}
