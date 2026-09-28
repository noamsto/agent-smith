package freshness

import (
	"strings"
	"testing"
)

// simple drops Text/Masked so table rows can assert on the parts that vary.
type simple struct {
	Form Form
	Path string
	Line int
}

func simplify(cands []Candidate) []simple {
	out := make([]simple, len(cands))
	for i, c := range cands {
		out[i] = simple{Form: c.Form, Path: c.Path, Line: c.Line}
	}
	return out
}

func TestExtract(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []simple
	}{
		{
			name:    "basic import",
			content: "See @docs/guide.md for details.",
			want:    []simple{{FormImport, "docs/guide.md", 1}},
		},
		{
			name:    "import trims trailing punctuation",
			content: "Read @internal/freshness/extract.go, then test it.",
			want:    []simple{{FormImport, "internal/freshness/extract.go", 1}},
		},
		{
			name:    "import at start of line",
			content: "@AGENTS.shared.md",
			want:    []simple{{FormImport, "AGENTS.shared.md", 1}},
		},
		{
			name:    "email address is not an import",
			content: "Contact noam@example.com for help.",
			want:    nil,
		},
		{
			name:    "link with title stripped",
			content: `[guide](docs/guide.md "Guide")`,
			want:    []simple{{FormLink, "docs/guide.md", 1}},
		},
		{
			name:    "link with angle brackets and space",
			content: "[guide](<docs/guide 2.md>)",
			want:    []simple{{FormLink, "docs/guide 2.md", 1}},
		},
		{
			name:    "link fragment stripped",
			content: "[section](docs/guide.md#section)",
			want:    []simple{{FormLink, "docs/guide.md", 1}},
		},
		{
			name:    "pure fragment link ignored",
			content: "[toc](#section)",
			want:    nil,
		},
		{
			name:    "image link",
			content: "![diagram](images/pic.png)",
			want:    []simple{{FormLink, "images/pic.png", 1}},
		},
		{
			name:    "basic backtick path",
			content: "Edit `internal/freshness/extract.go` next.",
			want:    []simple{{FormBacktick, "internal/freshness/extract.go", 1}},
		},
		{
			name:    "backtick line ref :N stripped",
			content: "See `cluster.go:12` for the query.",
			want:    []simple{{FormBacktick, "cluster.go", 1}},
		},
		{
			name:    "backtick line ref :N-M stripped",
			content: "See `cluster.go:12-30` for the query.",
			want:    []simple{{FormBacktick, "cluster.go", 1}},
		},
		{
			name:    "backtick line ref :N:C stripped",
			content: "See `cluster.go:12:4` for the query.",
			want:    []simple{{FormBacktick, "cluster.go", 1}},
		},
		{
			name:    "backtick line ref #LN stripped",
			content: "See `cluster.go#L12` for the query.",
			want:    []simple{{FormBacktick, "cluster.go", 1}},
		},
		{
			name:    "backtick line ref #LN-LM stripped",
			content: "See `cluster.go#L12-L30` for the query.",
			want:    []simple{{FormBacktick, "cluster.go", 1}},
		},
		{
			name:    "backtick line ref #LN-M stripped",
			content: "See `cluster.go#L12-30` for the query.",
			want:    []simple{{FormBacktick, "cluster.go", 1}},
		},
		{
			name:    "backtick content with whitespace ignored",
			content: "Run `go test ./...` before committing.",
			want:    nil,
		},
		{
			name:    "double-backtick span",
			content: "See ``pair/here.go`` for details.",
			want:    []simple{{FormBacktick, "pair/here.go", 1}},
		},
		{
			name:    "double-backtick span with padded literal backticks",
			content: "See `` `verbatim` `` marker.",
			want:    []simple{{FormBacktick, "`verbatim`", 1}},
		},
		{
			name:    "unclosed backtick run is literal text, later span still parses",
			content: "a stray ` backtick with no partner, but a ``pair/here.go`` exists",
			want:    []simple{{FormBacktick, "pair/here.go", 1}},
		},
		{
			name:    "import inside code span is ignored",
			content: "Use `@scope/pkg` for imports.",
			want:    []simple{{FormBacktick, "@scope/pkg", 1}},
		},
		{
			name:    "dedup identical form and path on one line",
			content: "See `cluster.go` and again `cluster.go` here.",
			want:    []simple{{FormBacktick, "cluster.go", 1}},
		},
		{
			name: "1-based line numbers across multiple lines",
			content: "first line\n" +
				"@second/line.md\n" +
				"third `line.go` here",
			want: []simple{
				{FormImport, "second/line.md", 2},
				{FormBacktick, "line.go", 3},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := simplify(Extract(tt.content))
			if len(got) != len(tt.want) {
				t.Fatalf("Extract(%q) = %+v, want %+v", tt.content, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("Extract(%q)[%d] = %+v, want %+v", tt.content, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractFences(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []simple
	}{
		{
			name: "backtick fence skips its contents",
			content: "before `real.go`\n" +
				"```\n" +
				"@fenced/import.md\n" +
				"`fenced/backtick.go`\n" +
				"```\n" +
				"after `real2.go`",
			want: []simple{
				{FormBacktick, "real.go", 1},
				{FormBacktick, "real2.go", 6},
			},
		},
		{
			name: "tilde fence skips its contents",
			content: "before `real.go`\n" +
				"~~~\n" +
				"`fenced/backtick.go`\n" +
				"~~~\n" +
				"after `real2.go`",
			want: []simple{
				{FormBacktick, "real.go", 1},
				{FormBacktick, "real2.go", 5},
			},
		},
		{
			name: "indented fence still toggles",
			content: "before `real.go`\n" +
				"   ```\n" +
				"   `fenced/backtick.go`\n" +
				"   ```\n" +
				"after `real2.go`",
			want: []simple{
				{FormBacktick, "real.go", 1},
				{FormBacktick, "real2.go", 5},
			},
		},
		{
			name: "longer closing run required",
			content: "````\n" +
				"``` still inside\n" +
				"````\n" +
				"after `real.go`",
			want: []simple{
				{FormBacktick, "real.go", 4},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := simplify(Extract(tt.content))
			if len(got) != len(tt.want) {
				t.Fatalf("Extract(%q) = %+v, want %+v", tt.content, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("Extract(%q)[%d] = %+v, want %+v", tt.content, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractTextAndMasked(t *testing.T) {
	cands := Extract("  See `config.yaml` for e.g. settings.  ")
	if len(cands) != 1 {
		t.Fatalf("Extract = %+v, want 1 candidate", cands)
	}
	c := cands[0]
	wantText := "See `config.yaml` for e.g. settings."
	if c.Text != wantText {
		t.Fatalf("Text = %q, want %q", c.Text, wantText)
	}
	wantMasked := "See " + strings.Repeat(" ", len("`config.yaml`")) + " for e.g. settings."
	if c.Masked != wantMasked {
		t.Fatalf("Masked = %q, want %q", c.Masked, wantMasked)
	}
}
