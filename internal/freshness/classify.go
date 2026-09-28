package freshness

import (
	"regexp"
	"strings"
)

// Class is the static confidence bucket a Candidate falls into before any
// filesystem lookup. Skip means never flagged; Ambiguous means flagged only
// after adjudication; Confident resolves directly against the filesystem.
type Class int

const (
	Skip Class = iota
	Ambiguous
	Confident
)

// skipChars are metacharacters that mark a token as a shell variable, glob,
// placeholder, or quoted/parenthesized prose rather than a literal path.
const skipChars = `$*?[]{}<>|='"(),\`

// exampleMarkerRe flags a line that is introducing a placeholder or sample,
// not naming a real path — "e.g. `config.yaml`", "such as `foo/bar.go`".
var exampleMarkerRe = regexp.MustCompile(`(?i)\be\.g\.|\bi\.e\.|\bexample|\bsuch as\b`)

// hostSegRe matches a scheme-less URL's leading host (`github.com/o/r`,
// `pkg.go.dev/fmt`). Requiring the "/" keeps a bare `install.sh` or
// `libfoo.so` a file.
var hostSegRe = regexp.MustCompile(`(?i)^[a-z0-9-]+(\.[a-z0-9-]+)*\.(com|org|net|io|dev|ai|app|sh|co|me|gg|so|xyz|cloud|run)/`)

var placeholderStems = map[string]bool{"foo": true, "bar": true, "baz": true, "qux": true, "xxx": true}

// Classify applies the filesystem-independent rules; existence, liveness and
// gitignore checks happen during resolution.
func Classify(c Candidate) Class {
	if skipAlways(c.Path) {
		return Skip
	}
	if c.Form == FormBacktick && skipBacktickOnly(c.Path) {
		return Skip
	}

	last := lastSegment(c.Path)
	switch c.Form {
	case FormBacktick:
		noExt := !hasExt(last) && !strings.HasSuffix(c.Path, "/") &&
			!strings.HasPrefix(c.Path, "./") && !strings.HasPrefix(c.Path, "../")
		if noExt || exampleMarkerRe.MatchString(c.Masked) {
			return Ambiguous
		}
	case FormImport:
		if !hasExt(last) {
			return Ambiguous
		}
	}
	return Confident
}

func skipAlways(path string) bool {
	if path == "" {
		return true
	}
	if strings.Contains(path, "://") || strings.HasPrefix(path, "//") || hostSegRe.MatchString(path) {
		return true
	}
	if strings.HasPrefix(path, "mailto:") || strings.HasPrefix(path, "data:") || strings.HasPrefix(path, "tel:") {
		return true
	}
	if strings.ContainsAny(path, skipChars) || strings.Contains(path, "…") || strings.Contains(path, "...") {
		return true
	}
	if strings.HasPrefix(path, "-") {
		return true
	}
	if path == "~" || (strings.HasPrefix(path, "~") && path[1] != '/') {
		return true
	}
	if hasPlaceholderSegment(path) {
		return true
	}
	if strings.Contains(path, "path/to/") {
		return true
	}
	if path == "/tmp" || strings.HasPrefix(path, "/tmp/") {
		return true
	}
	return false
}

func skipBacktickOnly(path string) bool {
	return !strings.Contains(path, "/") || strings.Contains(path, "@")
}

// hasPlaceholderSegment reports whether any "/"-segment's stem (the part
// before its first ".") is a placeholder like foo/bar/baz/qux/xxx.
func hasPlaceholderSegment(path string) bool {
	for _, seg := range strings.Split(path, "/") {
		stem := seg
		if i := strings.Index(seg, "."); i >= 0 {
			stem = seg[:i]
		}
		if placeholderStems[strings.ToLower(stem)] {
			return true
		}
	}
	return false
}

// lastSegment returns the last "/"-segment of path after trimming a trailing "/".
func lastSegment(path string) string {
	p := strings.TrimRight(path, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// hasExt reports whether seg is a hidden file (starts with "." and has more
// after it) or ends with a "." followed by 1-10 ASCII alphanumerics.
func hasExt(seg string) bool {
	if strings.HasPrefix(seg, ".") && len(seg) > 1 {
		return true
	}
	idx := strings.LastIndex(seg, ".")
	if idx <= 0 {
		return false
	}
	ext := seg[idx+1:]
	if len(ext) < 1 || len(ext) > 10 {
		return false
	}
	for i := 0; i < len(ext); i++ {
		c := ext[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}
