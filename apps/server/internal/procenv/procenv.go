package procenv

import (
	"os"
	"runtime"
	"strings"
)

// Safe returns only operating-system variables required to launch ordinary
// child processes. Application configuration, credentials and service secrets
// are deliberately not inherited by Git or deployment commands.
func Safe() []string {
	allowed := map[string]bool{
		"PATH": true, "LANG": true, "TZ": true, "TMPDIR": true,
	}
	if runtime.GOOS == "windows" {
		for _, key := range []string{"SystemRoot", "WINDIR", "COMSPEC", "PATHEXT", "TEMP", "TMP", "USERPROFILE", "APPDATA", "LOCALAPPDATA"} {
			allowed[strings.ToUpper(key)] = true
		}
	}
	out := make([]string, 0, len(allowed))
	for _, item := range os.Environ() {
		key, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		lookup := key
		if runtime.GOOS == "windows" {
			lookup = strings.ToUpper(key)
		}
		if allowed[lookup] || strings.HasPrefix(lookup, "LC_") {
			out = append(out, item)
		}
	}
	return out
}
