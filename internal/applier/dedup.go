package applier

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/noamsto/agent-smith/internal/analyst"
)

// dedupKey identifies one artifact+behavior so duplicate pending work across
// mining runs collapses to a single key. It is the canonical artifact file path
// plus the normalized "#section" anchor that names the behavior/signal. The two
// 06-04 and 06-07 skeleton-first proposals share both, so they share a key even
// though their ids, slugs, and reason-log filenames differ.
type dedupKey struct {
	path    string
	section string
}

func (k dedupKey) String() string {
	if k.section == "" {
		return k.path
	}
	return k.path + "#" + k.section
}

// proposalKey derives the dedup key for a resolved proposal. The file path is the
// already-canonicalized Target.FilePath (symlinks followed, worktree remapped to
// the main repo), so two artifacts that point at the same on-disk file via
// different worktrees collapse together.
func proposalKey(p analyst.Proposal, filePath string) dedupKey {
	_, section := splitArtifact(p.ImplicatedArtifact)
	return dedupKey{path: filePath, section: slug(section)}
}

// PullRequest is the slice of `gh pr list --json number,title,headRefName,url`
// output the dedup gate needs. Injected so tests run offline.
type PullRequest struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	HeadRefName string `json:"headRefName"`
	URL         string `json:"url"`
}

// ListOpenPRs is a source of this repo's open PRs. The production implementation
// shells out to `gh`; tests inject a fixed slice.
type ListOpenPRs func() ([]PullRequest, error)

// GhOpenPRs returns the open PRs for the repo rooted at dir via the gh CLI.
func GhOpenPRs(run runner, dir string) ListOpenPRs {
	return func() ([]PullRequest, error) {
		out, err := run(dir, "gh", "pr", "list", "--state", "open",
			"--json", "number,title,headRefName,url")
		if err != nil {
			return nil, fmt.Errorf("gh pr list: %w: %s", err, out)
		}
		var prs []PullRequest
		if err := json.Unmarshal(out, &prs); err != nil {
			return nil, fmt.Errorf("gh pr list: parse: %w", err)
		}
		return prs, nil
	}
}

// reasonLogEntry is a prior diagnosis on disk that opened a PR. While the deja-vu
// outcome placeholder is still present the PR's fate is unknown, so the entry is
// pending and blocks a duplicate. Once deja-vu replaces the placeholder with an
// outcome the entry is resolved and no longer blocks — a re-proposal of a
// rejected fix is issue #4's territory, not #15's pending-work dedup.
type reasonLogEntry struct {
	file     string
	heading  string
	key      dedupKey
	prURL    string
	resolved bool
}

const outcomePlaceholder = "<!-- outcome appended by deja-vu -->"

var artifactLinePrefix = "**Artifact:** "

// scanReasonLog reads every entry in dir and returns the ones that have a PR link
// (i.e. became pending work). Entries without a PR link have not been submitted
// and so cannot be a pending duplicate.
func scanReasonLog(dir string) ([]reasonLogEntry, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, err
	}
	var out []reasonLogEntry
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		e := reasonLogEntry{file: path}
		for _, line := range strings.Split(content, "\n") {
			switch {
			case e.heading == "" && strings.HasPrefix(line, "# "):
				e.heading = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			case strings.HasPrefix(line, artifactLinePrefix):
				artifact := strings.TrimSpace(strings.TrimPrefix(line, artifactLinePrefix))
				p, section := splitArtifact(artifact)
				// Canonicalize the same way Resolve does so a prior entry that stored
				// the symlinked artifact path keys identically to a fresh proposal
				// that resolves the same file. Fall back to the raw path if it no
				// longer resolves (e.g. the artifact was since removed).
				if real, rerr := resolveRealPath(p); rerr == nil {
					p = real
				}
				e.key = dedupKey{path: p, section: slug(section)}
			case strings.HasPrefix(line, "**PR:** "):
				e.prURL = strings.TrimSpace(strings.TrimPrefix(line, "**PR:** "))
			}
		}
		if e.prURL == "" {
			continue // never submitted — not pending work
		}
		e.resolved = !strings.Contains(content, outcomePlaceholder) // placeholder gone ⇒ deja-vu recorded an outcome
		out = append(out, e)
	}
	return out, nil
}

// dedupGate decides whether a resolved proposal duplicates pending work. It
// reports a non-empty `supersedes` describing the prior PR/entry on a hit; an
// empty string means no duplicate. It checks, in order: an open PR whose head
// branch is the group branch this proposal would push to (artifact-derived, so a
// re-proposal on the same artifact matches); then a prior reason-log entry sharing
// the artifact+behavior key that linked a PR and has not yet been resolved.
//
// The reason-log key match is keyed on canonical artifact path + behavior anchor,
// so it catches the cross-run case the open-PR branch check misses: a different
// proposal id (different slug, different branch) diagnosing the SAME artifact and
// behavior — exactly the 06-04 vs 06-07 skeleton-first collision.
func dedupGate(p analyst.Proposal, tg Target, openPRs []PullRequest, prior []reasonLogEntry) string {
	for _, pr := range openPRs {
		if pr.HeadRefName == tg.BranchName {
			return fmt.Sprintf("open PR #%d (%s)", pr.Number, pr.URL)
		}
	}
	key := proposalKey(p, tg.FilePath)
	for _, e := range prior {
		if e.resolved {
			continue
		}
		if e.key == key && e.heading != p.ID {
			return fmt.Sprintf("reason-log entry %q (PR %s)", e.heading, e.prURL)
		}
	}
	return ""
}
