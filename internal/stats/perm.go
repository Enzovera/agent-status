package stats

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

// permTarget splits a permission request's Detail ("zgoda: Bash: npm test")
// into the tool and a normalized pattern that groups similar requests:
//
//   - shell tools keep at most the first 2 words of the command, stopping at
//     a flag, a path or shell syntax ("Bash npm test", "Bash git");
//   - URLs become the domain ("WebFetch domain:example.com");
//   - any other argument (usually a path) is dropped: just the tool.
func permTarget(detail string) (tool, pattern string) {
	d := strings.TrimSpace(detail)
	d = strings.TrimSpace(strings.TrimPrefix(d, "zgoda:"))
	if d == "" {
		return "(nieznane)", "(nieznane)"
	}
	name, rest, ok := strings.Cut(d, ": ")
	if !ok {
		if !strings.ContainsAny(d, " \t") {
			return d, d
		}
		// Free text (an OpenCode permission message): group by its first words.
		return "(inne)", "(inne) " + firstWords(d, 3)
	}
	if strings.ContainsAny(name, " \t") || utf8.RuneCountInString(name) > 64 {
		return "(inne)", "(inne) " + firstWords(d, 3)
	}
	rest = strings.TrimSpace(rest)
	if u, err := url.Parse(rest); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return name, name + " domain:" + u.Hostname()
	}
	if !isShell(name) {
		return name, name
	}
	var keep []string
	for _, w := range strings.Fields(rest) {
		if len(keep) == 2 || stopWord(w) {
			break
		}
		keep = append(keep, w)
	}
	if len(keep) == 0 {
		return name, name
	}
	return name, name + " " + strings.Join(keep, " ")
}

func isShell(tool string) bool {
	switch strings.ToLower(tool) {
	case "bash", "shell", "sh", "powershell", "pwsh", "cmd", "terminal", "exec", "run":
		return true
	}
	return false
}

// stopWord: a flag, a path, a file name or shell syntax ends the pattern.
func stopWord(w string) bool {
	if strings.HasPrefix(w, "-") || strings.HasPrefix(w, "~") || strings.HasPrefix(w, ".") {
		return true
	}
	if strings.ContainsAny(w, `/\.:"'$|&;<>()=*{}[]`+"`") {
		return true
	}
	return false
}

func firstWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		f = f[:n]
	}
	return strings.Join(f, " ")
}
