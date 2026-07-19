package gittest

import (
	"os/exec"
	"regexp"
	"strconv"
)

var versionRE = regexp.MustCompile(`(\d+)\.(\d+)`)

// SupportsSSHSigning reports whether the git binary on PATH understands
// gpg.format=ssh, which was introduced in git 2.34. Older gits reject the
// config with "unsupported value for gpg.format" the moment a signing
// operation runs, so SSH-signing fixtures must skip there.
func SupportsSSHSigning() bool {
	major, minor, ok := gitVersion()
	return ok && (major > 2 || (major == 2 && minor >= 34))
}

// gitVersion parses `git version` output ("git version 2.34.1", with possible
// platform suffixes like "2.47.0.windows.1") into (major, minor).
func gitVersion() (major, minor int, ok bool) {
	out, err := exec.Command("git", "version").Output()
	if err != nil {
		return 0, 0, false
	}
	m := versionRE.FindStringSubmatch(string(out))
	if m == nil {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, true
}
