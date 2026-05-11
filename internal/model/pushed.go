package model

// AnyPushed reports whether any of the given commits already exists on a remote
// (its IsPushed flag, computed against all remote-tracking refs at read time).
// It is the single pushed-detection predicate: every mutating surface passes the
// commits it will actually rewrite — including descendants, since rewriting a
// commit re-hashes everything after it — so redate/split/squash/reword agree on
// when a rewrite touches published history and needs the typed confirmation.
func AnyPushed(commits []Commit) bool {
	for _, c := range commits {
		if c.IsPushed {
			return true
		}
	}
	return false
}
