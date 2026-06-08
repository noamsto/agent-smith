package applier

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/noamsto/agent-smith/internal/analyst"
)

// PRStatus is one PR's resolved state, as reported by `gh pr list --state all`.
type PRStatus struct {
	URL   string `json:"url"`
	State string `json:"state"` // gh: OPEN | MERGED | CLOSED
}

// ghState maps a gh PR state to a reason-log outcome. A CLOSED-not-merged PR is
// the rejection signal the deja-vu loop must remember.
func ghState(state string) string {
	switch strings.ToUpper(state) {
	case "MERGED":
		return analyst.OutcomeMerged
	case "CLOSED":
		return analyst.OutcomeClosed
	default:
		return analyst.OutcomeOpen
	}
}

// prURLRe-free extraction: the PR line the applier wrote is "**PR:** <url>".
func entryPRURL(content string) string {
	const marker = "**PR:** "
	i := strings.Index(content, marker)
	if i < 0 {
		return ""
	}
	rest := content[i+len(marker):]
	if j := strings.IndexByte(rest, '\n'); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

// Reconcile walks the reason-log dir and updates each entry's outcome marker to
// match the resolved state of its recorded PR. Entries with no PR link, no
// matching status, or an already-current outcome are left untouched. It returns
// the number of entries updated. PR statuses are injected so the caller owns the
// `gh` query and tests stay offline.
func Reconcile(dir string, statuses []PRStatus) (int, error) {
	byURL := make(map[string]string, len(statuses))
	for _, s := range statuses {
		byURL[s.URL] = ghState(s.State)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return 0, fmt.Errorf("glob %s: %w", dir, err)
	}
	updated := 0
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return updated, fmt.Errorf("read %s: %w", path, err)
		}
		content := string(data)
		url := entryPRURL(content)
		if url == "" {
			continue
		}
		want, ok := byURL[url]
		if !ok || want == analyst.OutcomeOpen {
			continue
		}
		next, changed := analyst.SetOutcome(content, want)
		if !changed || next == content {
			continue
		}
		if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
			return updated, fmt.Errorf("write %s: %w", path, err)
		}
		updated++
	}
	return updated, nil
}

// EntryRepos returns the distinct GitHub "owner/repo" slugs referenced by the PR
// links across the reason-log dir, so the caller can query each repo once.
func EntryRepos(dir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("glob %s: %w", dir, err)
	}
	seen := map[string]bool{}
	var repos []string
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		repo := repoSlug(entryPRURL(string(data)))
		if repo == "" || seen[repo] {
			continue
		}
		seen[repo] = true
		repos = append(repos, repo)
	}
	return repos, nil
}

// repoSlug extracts "owner/repo" from a github.com PR URL.
func repoSlug(prURL string) string {
	const host = "https://github.com/"
	if !strings.HasPrefix(prURL, host) {
		return ""
	}
	parts := strings.SplitN(strings.TrimPrefix(prURL, host), "/", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// FetchPRStatuses runs `gh pr list --state all -R repo` and decodes the URL+state
// of every PR. It is the production source for Reconcile's injected statuses.
func FetchPRStatuses(run runner, repo string) ([]PRStatus, error) {
	out, err := run("", "gh", "pr", "list", "-R", repo, "--state", "all",
		"--limit", "1000", "--json", "url,state")
	if err != nil {
		return nil, fmt.Errorf("gh pr list %s: %w", repo, err)
	}
	var statuses []PRStatus
	if err := json.Unmarshal(out, &statuses); err != nil {
		return nil, fmt.Errorf("decode gh pr list %s: %w", repo, err)
	}
	return statuses, nil
}
