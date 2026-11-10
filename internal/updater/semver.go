package updater

import (
	"strconv"
	"strings"
)

// semver is a release version MAJOR.MINOR.PATCH.
type semver [3]int

// parseVersion parses "1.2.3" or "v1.2.3". Anything else — "dev", git
// describe output like "2.1.0-3-gabc" or "2.1.0-dirty", pre-releases like
// "2.1.0-rc1" — is rejected, so such builds never auto-update and are never
// offered as an update.
func parseVersion(s string) (semver, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var v semver
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || (len(p) > 1 && p[0] == '0') {
			return semver{}, false
		}
		v[i] = n
	}
	return v, true
}

func (a semver) less(b semver) bool {
	for i := range 3 {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func (a semver) String() string {
	return strconv.Itoa(a[0]) + "." + strconv.Itoa(a[1]) + "." + strconv.Itoa(a[2])
}
