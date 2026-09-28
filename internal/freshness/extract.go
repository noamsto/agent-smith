// Package freshness audits instruction files for references to paths that no longer exist.
package freshness

import (
	"regexp"
	"strings"
	"unicode"
)

// Form identifies which markdown construct produced a Candidate.
type Form string

const (
	FormImport   Form = "import"
	FormLink     Form = "link"
	FormBacktick Form = "backtick"
)

// Candidate is a path-shaped reference found in an instruction file, not yet
// classified or resolved against the filesystem.
type Candidate struct {
	Form   Form
	Path   string
	Line   int    // 1-based
	Text   string // trimmed source line, used for excerpts
	Masked string // trimmed source line with code spans blanked, used for the example-marker check
}

var (
	// lineRefRe strips a trailing editor-style line reference from a backtick
	// path so `file.go:42` and `file.go` resolve to the same candidate.
	lineRefRe = regexp.MustCompile(`(:\d+(-\d+)?(:\d+)?|#L\d+(-L?\d+)?)$`)
	importRe  = regexp.MustCompile(`(?:^|\s)@(\S+)`)
	linkRe    = regexp.MustCompile(`!?\[[^\]]*\]\(\s*(<[^>]*>|[^)\s]+)(?:\s+"[^"]*")?\s*\)`)
)

// Extract scans markdown content line by line for @import, [link](target), and
// `backtick` path references, skipping fenced code blocks entirely.
func Extract(content string) []Candidate {
	var out []Candidate

	var inFence bool
	var fenceChar byte
	var fenceLen int

	for i, line := range strings.Split(content, "\n") {
		lineNum := i + 1

		if ch, n, ok := fenceMarker(line); ok {
			if inFence {
				if ch == fenceChar && n >= fenceLen {
					inFence = false
				}
			} else {
				inFence, fenceChar, fenceLen = true, ch, n
			}
			continue // fence-marker lines never yield candidates
		}
		if inFence {
			continue
		}

		out = append(out, extractLine(line, lineNum)...)
	}
	return out
}

// fenceMarker reports whether line opens or closes a ``` or ~~~ fence. Any
// indentation counts, beyond CommonMark's 3 spaces, so a fence nested in a list
// item is honored; misreading an indented code line as a fence only skips
// candidates, the safe direction. It doesn't distinguish open from close — the
// caller compares ch/n against the current fence state.
func fenceMarker(line string) (ch byte, n int, ok bool) {
	rest := strings.TrimLeft(line, " \t")
	switch {
	case strings.HasPrefix(rest, "```"):
		ch = '`'
	case strings.HasPrefix(rest, "~~~"):
		ch = '~'
	default:
		return 0, 0, false
	}
	for n < len(rest) && rest[n] == ch {
		n++
	}
	return ch, n, true
}

// span is one inline code span, in indices relative to the line it was found in.
type span struct {
	start, end               int // full span, delimiters included
	contentStart, contentEnd int
}

// codeSpans finds every inline code span in line: a run of N backticks up to
// the next run of exactly N backticks. A run with no matching close is literal
// text, not a delimiter, per CommonMark's inline-code-span rule.
func codeSpans(line string) []span {
	var spans []span
	i := 0
	for i < len(line) {
		if line[i] != '`' {
			i++
			continue
		}
		j := i
		for j < len(line) && line[j] == '`' {
			j++
		}
		n := j - i

		k, found := j, -1
		for k < len(line) {
			if line[k] != '`' {
				k++
				continue
			}
			m := k
			for m < len(line) && line[m] == '`' {
				m++
			}
			if m-k == n {
				found = k
				break
			}
			k = m
		}

		if found >= 0 {
			spans = append(spans, span{start: i, end: found + n, contentStart: j, contentEnd: found})
			i = found + n
		} else {
			i = j // unclosed run: treat the backticks as literal text
		}
	}
	return spans
}

// maskSpans replaces every code span, delimiters included, with spaces so
// downstream regexes never fire inside inline code.
func maskSpans(line string, spans []span) string {
	if len(spans) == 0 {
		return line
	}
	b := []byte(line)
	for _, s := range spans {
		for k := s.start; k < s.end; k++ {
			b[k] = ' '
		}
	}
	return string(b)
}

func extractLine(line string, lineNum int) []Candidate {
	text := strings.TrimSpace(line)
	spans := codeSpans(text)
	masked := maskSpans(text, spans)

	seen := map[[2]string]bool{}
	var out []Candidate
	add := func(form Form, path string) {
		if path == "" {
			return
		}
		key := [2]string{string(form), path}
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, Candidate{Form: form, Path: path, Line: lineNum, Text: text, Masked: masked})
	}

	for _, s := range spans {
		content := text[s.contentStart:s.contentEnd]
		if len(content) >= 2 && content[0] == ' ' && content[len(content)-1] == ' ' {
			content = content[1 : len(content)-1]
		}
		content = strings.TrimSpace(content)
		if content == "" || strings.ContainsFunc(content, unicode.IsSpace) {
			continue
		}
		add(FormBacktick, lineRefRe.ReplaceAllString(content, ""))
	}

	for _, m := range importRe.FindAllStringSubmatch(masked, -1) {
		add(FormImport, strings.TrimRight(m[1], ".,;:)"))
	}

	for _, m := range linkRe.FindAllStringSubmatch(masked, -1) {
		target := m[1]
		if strings.HasPrefix(target, "<") && strings.HasSuffix(target, ">") {
			target = target[1 : len(target)-1]
		}
		if idx := strings.Index(target, "#"); idx >= 0 {
			target = target[:idx]
		}
		add(FormLink, target)
	}

	return out
}
