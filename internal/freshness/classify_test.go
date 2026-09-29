package freshness

import "testing"

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		c    Candidate
		want Class
	}{
		// --- Skip, all forms ---
		{"skip: URL scheme", Candidate{Form: FormLink, Path: "https://example.com/docs"}, Skip},
		{"skip: mailto", Candidate{Form: FormLink, Path: "mailto:foo@bar.com"}, Skip},
		{"skip: data URI", Candidate{Form: FormLink, Path: "data:text/plain;base64,abc"}, Skip},
		{"skip: tel", Candidate{Form: FormLink, Path: "tel:+15551234567"}, Skip},
		{"skip: dollar variable", Candidate{Form: FormBacktick, Path: "$HOME/file.go"}, Skip},
		{"skip: glob metacharacter", Candidate{Form: FormBacktick, Path: "src/*.go"}, Skip},
		{"skip: angle bracket placeholder", Candidate{Form: FormBacktick, Path: "<repo>/file.go"}, Skip},
		{"skip: backslash", Candidate{Form: FormBacktick, Path: `foo\bar.go`}, Skip},
		{"skip: ellipsis substring", Candidate{Form: FormBacktick, Path: "src/.../file.go"}, Skip},
		{"skip: ellipsis unicode char", Candidate{Form: FormBacktick, Path: "src/…/file.go"}, Skip},
		{"skip: leading dash flag", Candidate{Form: FormBacktick, Path: "-flag/file.go"}, Skip},
		{"skip: bare tilde", Candidate{Form: FormBacktick, Path: "~"}, Skip},
		{"skip: tilde-user", Candidate{Form: FormBacktick, Path: "~noam/file.go"}, Skip},
		{"skip: placeholder segment foo", Candidate{Form: FormBacktick, Path: "src/foo/bar.go"}, Skip},
		{"skip: placeholder segment case-insensitive", Candidate{Form: FormBacktick, Path: "src/FOO/bar.go"}, Skip},
		{"skip: path/to/ literal", Candidate{Form: FormLink, Path: "see path/to/file.go"}, Skip},
		{"skip: exactly /tmp", Candidate{Form: FormBacktick, Path: "/tmp"}, Skip},
		{"skip: /tmp/ prefix", Candidate{Form: FormBacktick, Path: "/tmp/file.go"}, Skip},
		{"skip: empty path", Candidate{Form: FormBacktick, Path: ""}, Skip},
		{"skip: scheme-relative URL", Candidate{Form: FormLink, Path: "//cdn.example.com/x.js"}, Skip},
		{"skip: scheme-less host link", Candidate{Form: FormLink, Path: "example.com/x.md"}, Skip},
		{"skip: go module path", Candidate{Form: FormBacktick, Path: "github.com/o/r"}, Skip},
		{"skip: multi-label host", Candidate{Form: FormBacktick, Path: "pkg.go.dev/fmt"}, Skip},
		{"skip: host case-insensitive", Candidate{Form: FormLink, Path: "GitHub.COM/o/r.md"}, Skip},

		// --- Skip, backtick only ---
		{"skip: backtick no slash", Candidate{Form: FormBacktick, Path: "README"}, Skip},
		{"skip: backtick contains @", Candidate{Form: FormBacktick, Path: "foo/bar@baz"}, Skip},

		// --- Ambiguous ---
		{"ambiguous: backtick no extension", Candidate{Form: FormBacktick, Path: "origin/main"}, Ambiguous},
		{
			"ambiguous: backtick example marker in masked line",
			Candidate{Form: FormBacktick, Path: "config/settings.yaml", Masked: "see e.g. a config file for reference"},
			Ambiguous,
		},
		{"ambiguous: import no extension", Candidate{Form: FormImport, Path: "me"}, Ambiguous},

		// --- Confident ---
		{"confident: backtick with extension", Candidate{Form: FormBacktick, Path: "internal/freshness/extract.go"}, Confident},
		{"confident: import with extension", Candidate{Form: FormImport, Path: "shared/lib.md"}, Confident},
		{"confident: link never goes through the ambiguous rule", Candidate{Form: FormLink, Path: "docs/guide"}, Confident},
		{"confident: backtick trailing slash is not ambiguous", Candidate{Form: FormBacktick, Path: "internal/freshness/"}, Confident},
		{"confident: backtick ./ prefix is not ambiguous", Candidate{Form: FormBacktick, Path: "./Makefile"}, Confident},
		{"confident: backtick ../ prefix is not ambiguous", Candidate{Form: FormBacktick, Path: "../Makefile"}, Confident},
		{"confident: bare .sh link is a file, not a host", Candidate{Form: FormLink, Path: "install.sh"}, Confident},
		{"confident: bare .ai link is a file, not a host", Candidate{Form: FormLink, Path: "logo.ai"}, Confident},
		{"confident: TLD-like extension in a later segment", Candidate{Form: FormBacktick, Path: "scripts/install.sh"}, Confident},
		{"confident: .md first segment", Candidate{Form: FormLink, Path: "notes.md"}, Confident},
		{"confident: .go file under a dir", Candidate{Form: FormBacktick, Path: "docs/x.go"}, Confident},
		{
			// The candidate's own span is masked out of Masked, so a path literally
			// named "examples/..." doesn't trip the example-marker rule on itself.
			"confident: masked span hides the path's own 'example' text",
			Candidate{Form: FormBacktick, Path: "examples/config.yaml", Masked: "see    for details"},
			Confident,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.c); got != tt.want {
				t.Fatalf("Classify(%+v) = %v, want %v", tt.c, got, tt.want)
			}
		})
	}
}
