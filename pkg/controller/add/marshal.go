package add

import (
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
	"github.com/aquaproj/ar2/pkg/g2"
)

// marshal renders the definition the way the registry stores one. See g2.MarshalConfig.
func marshal(cfg *aquag2.Config) (string, error) {
	return g2.MarshalConfig(cfg) //nolint:wrapcheck // the error says what it couldn't marshal
}
