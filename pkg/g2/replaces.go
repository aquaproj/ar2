package g2

// ReplacesPublishedLabel is the label a person puts on a pull request that changes or removes
// a file the registry already serves. The checks fail without it, and count it only when a
// person put it on: a label the pull request's author can put on itself would decide nothing.
const ReplacesPublishedLabel = "replaces-published"

// ReplacesPublished is the part of a pull request's description saying that it waits for the
// label and what to have read before putting it on.
//
// The checks enforce the label, but a pull request that only fails says nothing about why or
// what the label means. The description is where whoever opens it reads that.
func ReplacesPublished(read string) string {
	return "## Merging this\n\n" +
		"This changes files the registry already serves, which a lock file may hold the checksums of. " +
		"The checks fail until a person labels it `" + ReplacesPublishedLabel + "`, and putting the label " +
		"on runs them again. A label a bot puts on doesn't count.\n\n" +
		"The label says that the change was read and is meant. Before putting it on, read " + read + "\n"
}
