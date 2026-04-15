package portal

import "regexp"

var safeNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func isValidName(name string) bool {
	return name != "" && len(name) <= 64 && safeNameRe.MatchString(name)
}
