package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoFile(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return b
}

func TestPluginManifest(t *testing.T) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(repoFile(t, ".claude-plugin/plugin.json"), &p); err != nil {
		t.Fatalf("plugin.json: %v", err)
	}
	if p.Name == "" {
		t.Error("plugin.json: name is empty")
	}
}

func TestMarketplaceManifest(t *testing.T) {
	var m struct {
		Name  string `json:"name"`
		Owner struct {
			Name string `json:"name"`
		} `json:"owner"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(repoFile(t, ".claude-plugin/marketplace.json"), &m); err != nil {
		t.Fatalf("marketplace.json: %v", err)
	}
	if m.Name == "" || m.Owner.Name == "" || len(m.Plugins) == 0 || m.Plugins[0].Name == "" {
		t.Errorf("marketplace.json missing required fields: %+v", m)
	}
}

func TestAgentsHaveFrontmatter(t *testing.T) {
	for _, a := range []string{"agents/oracle.md", "agents/editor.md"} {
		s := string(repoFile(t, a))
		if !strings.HasPrefix(s, "---\n") {
			t.Errorf("%s: missing opening frontmatter", a)
			continue
		}
		if !strings.Contains(s[len("---\n"):], "\n---") {
			t.Errorf("%s: missing closing frontmatter", a)
		}
		if !strings.Contains(s, "description:") {
			t.Errorf("%s: frontmatter missing description", a)
		}
	}
}

func TestCommandsExist(t *testing.T) {
	for _, c := range []string{"run", "mine", "propose", "apply", "status"} {
		rel := "commands/" + c + ".md"
		s := string(repoFile(t, rel))
		if len(s) == 0 {
			t.Errorf("%s is empty", rel)
			continue
		}
		if !strings.HasPrefix(s, "---\n") || !strings.Contains(s, "description:") {
			t.Errorf("%s: missing frontmatter description", rel)
		}
	}
}
