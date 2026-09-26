package add

import (
	"bytes"
	"fmt"

	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"go.yaml.in/yaml/v3"
)

// yamlIndent is how far a registry file indents, which is what aqua-registry uses.
const yamlIndent = 2

// marshal renders the definition the way aqua-registry writes one.
//
// The indentation is set rather than left to the encoder, whose default is four spaces.
// Registry files are written and read by people, and one that doesn't look like the
// others is one more thing to notice.
func marshal(cfg *aquag2.Config) (string, error) {
	buf := &bytes.Buffer{}
	encoder := yaml.NewEncoder(buf)
	encoder.SetIndent(yamlIndent)
	if err := encoder.Encode(cfg); err != nil {
		return "", fmt.Errorf("marshal the package definition: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return "", fmt.Errorf("close the YAML encoder: %w", err)
	}
	return buf.String(), nil
}
