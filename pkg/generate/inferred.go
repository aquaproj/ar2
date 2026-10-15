package generate

// The kinds of signing an entry can carry, by the names the checks give them.
const (
	KindSLSAProvenance = "slsa_provenance"
	KindCosign         = "cosign"
	KindPackslip       = "packslip"
)

// signingKinds is the kinds of signing some entry of the file carries.
func signingKinds(reg *Registry) map[string]bool {
	kinds := map[string]bool{}
	for _, asset := range reg.Assets {
		if asset.SLSAProvenance != nil {
			kinds[KindSLSAProvenance] = true
		}
		if asset.Cosign != nil {
			kinds[KindCosign] = true
		}
		if asset.Packslip != nil {
			kinds[KindPackslip] = true
		}
	}
	return kinds
}

// inferredKinds is the kinds of signing the file carries that none of its entries did
// before the release was read for them.
//
// A kind the definition wrote for some entry counts as written for all of them: whoever
// wrote it knew how the release is signed, and a check of it that fails is a release
// that isn't what it says.
func inferredKinds(declared map[string]bool, reg *Registry) map[string]bool {
	out := map[string]bool{}
	for kind := range signingKinds(reg) {
		if !declared[kind] {
			out[kind] = true
		}
	}
	return out
}

// DropSigning takes a kind of signing off every entry.
func DropSigning(reg *Registry, kind string) {
	for _, asset := range reg.Assets {
		switch kind {
		case KindSLSAProvenance:
			asset.SLSAProvenance = nil
		case KindCosign:
			asset.Cosign = nil
		case KindPackslip:
			asset.Packslip = nil
		}
	}
}
