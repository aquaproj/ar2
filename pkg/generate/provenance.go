package generate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// provenanceKind is what a provenance published beside a release turns out to be, which
// decides how aqua verifies it.
type provenanceKind int

const (
	// provenanceSLSA is one slsa-verifier verifies: built by slsa-github-generator. It is
	// also what a provenance that couldn't be read is taken for, which is what was assumed
	// of every one before they were read.
	provenanceSLSA provenanceKind = iota
	// provenanceAttestation is a GitHub artifact attestation: a Sigstore bundle a workflow
	// signed with its GitHub Actions identity, as actions/attest-build-provenance makes.
	// aqua verifies it with gh, which reads the same attestation from GitHub.
	provenanceAttestation
	// provenanceNone is a provenance that is neither, which nothing aqua runs verifies.
	provenanceNone
)

// provenance is what a provenance turned out to be.
type provenanceInfo struct {
	kind provenanceKind
	// signerWorkflow is the workflow that signed an attestation, as owner/repo/.github/workflows/file.
	signerWorkflow string
}

// slsaGeneratorBuilder is how the builder ids of slsa-github-generator's workflows begin.
// slsa-verifier trusts those and no other builder on GitHub.
const slsaGeneratorBuilder = "https://github.com/slsa-framework/slsa-github-generator/"

// sigstoreBundleType is how the media type of a Sigstore bundle begins.
const sigstoreBundleType = "application/vnd.dev.sigstore.bundle"

// rawProvenance is the part of a provenance file that says what it is: a DSSE envelope,
// either bare or inside a Sigstore bundle.
type rawProvenance struct {
	MediaType    string `json:"mediaType"`
	Payload      string `json:"payload"`
	DSSEEnvelope *struct {
		Payload string `json:"payload"`
	} `json:"dsseEnvelope"`
}

// statement is the part of an in-toto statement that says who built what it attests.
type statement struct {
	Predicate struct {
		Builder struct {
			ID string `json:"id"`
		} `json:"builder"`
		RunDetails struct {
			Builder struct {
				ID string `json:"id"`
			} `json:"builder"`
		} `json:"runDetails"`
	} `json:"predicate"`
}

// builderID is the builder the statement names, in either version of the SLSA predicate.
func (s *statement) builderID() string {
	if id := s.Predicate.RunDetails.Builder.ID; id != "" {
		return id
	}
	return s.Predicate.Builder.ID
}

// readProvenance reads what a provenance file is.
//
// A provenance named .intoto.jsonl says nothing about who made it. slsa-github-generator
// writes one, and so does a release workflow attesting its own build: awslabs/aidlc-workflows
// publishes the Sigstore bundle actions/attest-build-provenance made, which slsa-verifier
// doesn't trust and gh does. The first line is enough to tell: every line of the file is an
// attestation of the same build.
func readProvenance(b []byte) provenanceInfo {
	line, _, _ := bytes.Cut(bytes.TrimSpace(b), []byte("\n"))
	raw := &rawProvenance{}
	if err := json.Unmarshal(line, raw); err != nil {
		return provenanceInfo{kind: provenanceSLSA}
	}
	payload := raw.Payload
	if raw.DSSEEnvelope != nil {
		payload = raw.DSSEEnvelope.Payload
	}
	if strings.HasPrefix(builderOf(payload), slsaGeneratorBuilder) {
		return provenanceInfo{kind: provenanceSLSA}
	}
	if strings.HasPrefix(raw.MediaType, sigstoreBundleType) {
		if wf := attestingWorkflow(line); wf != "" {
			return provenanceInfo{kind: provenanceAttestation, signerWorkflow: wf}
		}
	}
	return provenanceInfo{kind: provenanceNone}
}

// builderOf is the builder an envelope's payload names, or "" when it can't be read.
func builderOf(payload string) string {
	b, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return ""
	}
	s := &statement{}
	if err := json.Unmarshal(b, s); err != nil {
		return ""
	}
	return s.builderID()
}

// attestingWorkflow is the workflow a bundle's certificate names, when GitHub Actions
// vouched for it: https://github.com/o/r/.github/workflows/release.yml@refs/tags/v1 is
// o/r/.github/workflows/release.yml.
func attestingWorkflow(bundle []byte) string {
	s, err := parseSigner(bundle)
	if err != nil || s.Issuer != githubOIDCIssuer {
		return ""
	}
	path, ok := strings.CutPrefix(s.Identity, "https://github.com/")
	if !ok {
		return ""
	}
	path, _, _ = strings.Cut(path, "@")
	if !strings.Contains(path, "/.github/workflows/") {
		return ""
	}
	return path
}

// provenances reads what each provenance of a release is, once each.
type provenances struct {
	ctx        context.Context //nolint:containedctx // it lives as long as one generation
	logger     *slog.Logger
	httpClient *http.Client
	base       string
	read       map[string]provenanceInfo
}

// newProvenances reads the provenances of the release at the version.
func (g *Generator) newProvenances(ctx context.Context, logger *slog.Logger, owner, name, version string) *provenances {
	return &provenances{
		ctx: ctx, logger: logger, httpClient: g.httpClient,
		base: fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/", owner, name, pathEscape(version)),
		read: map[string]provenanceInfo{},
	}
}

// of is what the provenance asset is. One that can't be downloaded is taken for SLSA's.
func (p *provenances) of(asset string) provenanceInfo {
	if got, ok := p.read[asset]; ok {
		return got
	}
	got := provenanceInfo{kind: provenanceSLSA}
	b, err := readSmallAsset(p.ctx, p.httpClient, p.base+pathEscape(asset))
	if err != nil {
		p.logger.Debug("couldn't read a provenance, so it is taken for SLSA's", "asset", asset, "error", err.Error())
	} else {
		got = readProvenance(b)
	}
	p.read[asset] = got
	return got
}
