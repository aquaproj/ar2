package validatedefinition

import "errors"

var (
	errEmpty  = errors.New("the definition says nothing")
	errNoName = errors.New("the definition doesn't name its package, which is the only thing that says which package the directory holds")
	errNoType = errors.New("the definition doesn't say where the package comes from")

	errAssetRuleNoEnvs = errors.New("an asset rule says no environment")
	errAssetRuleEnv    = errors.New("an asset rule's environment isn't os/arch")
)
