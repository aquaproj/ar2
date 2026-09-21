package generate

import (
	genrgst "github.com/aquaproj/aqua/v2/pkg/controller/generate-registry"
)

// inferSigning records, per environment, how the release signs that asset.
//
// A release says how it is signed by what it carries: a .sigstore.json, a .sig and
// a .pem, a cosign.pub. aqua gr reads the checksum file's signature that way, and
// the same reading applies to each asset when the generated file holds one entry per
// environment. The signer can't be read off a release, so the inferred configuration
// constrains the identity to a workflow in the package's own repository.
//
// A definition that already says how the asset is signed wins. It was written by
// someone who knows the release, and it can name a signer the inference has to
// generalize about.
//
// Only cosign is inferred. SLSA provenance already is, by aqua gr, from an asset
// ending in .intoto.jsonl. Minisign is not: a .minisig says a signature exists but
// not which public key it should verify against, and a signature checked against no
// particular key is not a check.
func inferSigning(pkgName string, reg *Registry, assetNames map[string]struct{}) error {
	owner, name, err := repo(pkgName)
	if err != nil {
		return err
	}
	for _, asset := range reg.Assets {
		if asset.Asset == "" || asset.Cosign != nil {
			continue
		}
		if cosign := genrgst.InferCosign(owner, name, asset.Asset, assetNames); cosign != nil {
			asset.Cosign = cosign
		}
	}
	return nil
}
