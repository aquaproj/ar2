package verify

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aquaproj/aqua/v2/pkg/unarchive"
	"github.com/aquaproj/ar2/pkg/generate"
)

// recordingSignatures records the assets whose signatures were checked.
type recordingSignatures struct {
	checked []string
}

func (r *recordingSignatures) Check(_ context.Context, _ *slog.Logger, _, _ string, asset *generate.Asset, _ string) error {
	r.checked = append(r.checked, asset.OS+"/"+asset.Arch)
	return nil
}

// An asset this machine can't open still has its signatures checked, and its checksum comes
// from the download that checked them: lxc/incus's darwin/amd64 .pkg, generated on Linux,
// was left saying it is attested without what the certificate records.
func TestComplete_signaturesOfWhatCantBeOpened(t *testing.T) {
	t.Parallel()
	if Extractable(unarchive.FormatPKG) {
		t.Skip("this machine can open a pkg, so the asset would be extracted")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "an asset")
	}))
	defer srv.Close()

	reg := &generate.Registry{Assets: []*generate.Asset{
		{OS: "darwin", Arch: "amd64", Type: "http", Format: unarchive.FormatPKG, URL: srv.URL + "/incus.pkg"},
	}}
	sigs := &recordingSignatures{}
	filled, err := New(srv.Client(), sigs).Complete(context.Background(), discardLogger(), "lxc/incus", "v7.5.1", reg, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs.checked) != 1 || sigs.checked[0] != "darwin/amd64" {
		t.Errorf("signatures checked for %v", sigs.checked)
	}
	if reg.Assets[0].Checksum == "" {
		t.Error("no checksum was recorded")
	}
	if filled.NeedsReview {
		t.Error("the entry was sent for review")
	}
}
