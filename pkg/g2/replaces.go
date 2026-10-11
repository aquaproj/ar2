package g2

// ReplacesPublishedLabel marks a pull request that changes or removes a file the registry
// already serves. ar2 puts it on such a pull request when opening it and doesn't set it to
// merge itself, and the checks fail one that is set to: what lets it merge is a person
// merging it, and the label is there to say so to whoever reads the list.
const ReplacesPublishedLabel = "replaces-published"

// ReplacesPublished is the part of a pull request's description saying that it is left for
// a person to merge and what to have read before merging it.
func ReplacesPublished(read string) string {
	return "## Merging this\n\n" +
		"This changes files the registry already serves, which a lock file may hold the checksums of. " +
		"It is labelled `" + ReplacesPublishedLabel + "` and never merges itself: the checks fail it while " +
		"auto-merge is on. Merging it is what says the change was read and is meant.\n\n" +
		"Before merging, read " + read + "\n"
}
