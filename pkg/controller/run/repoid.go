package run

import (
	"errors"
	"fmt"
)

// errRepoReplaced says the repository a package's name answers with is not the one the
// package was taken over from.
var errRepoReplaced = errors.New("the repository has another id than the definition records")

// checkRepoID holds the definition to the repository's id.
//
// A repository's name can be taken over -- deleted and created again, or transferred and
// replaced -- and its id can't. A definition recording one id while the name answers with
// another is a package whose releases now come from somebody else, and nothing is
// generated for it until a person has looked. A definition that records none yet gets the
// id when it is new, which is the moment the package is taken over; one the registry
// already holds gets it from the rewrite of what is published rather than from a version's
// pull request.
//
// A repository the sweep couldn't read has no id to compare with, and is left to the run
// that can.
func (c *Controller) checkRepoID(def *definition, repo string) error {
	if def == nil || def.config == nil {
		return nil
	}
	id := c.repoIDs[repo]
	if id == 0 {
		return nil
	}
	switch {
	case def.config.RepoID == 0:
		if !def.held {
			def.config.RepoID = id
		}
	case def.config.RepoID != id:
		return fmt.Errorf("%w: %s is %d, the definition says %d", errRepoReplaced, repo, id, def.config.RepoID)
	}
	return nil
}
