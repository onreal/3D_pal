package skills_test

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/margarit/printpipe/skills"
)

func TestEveryFileIsListedAndReadable(t *testing.T) {
	listed, names := map[string]bool{}, map[string]bool{}
	for _, d := range skills.Docs() {
		if _, err := skills.Read(d.Path); err != nil {
			t.Errorf("%s: %v", d.Name, err)
		}
		if listed[d.Path] || names[d.Name] {
			t.Errorf("%s (%s) listed twice", d.Path, d.Name)
		}
		names[d.Name] = true
		listed[d.Path] = true
	}
	fs.WalkDir(skills.FS(), ".", func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() && !listed[p] {
			t.Errorf("%s is in the skill but not served: add it to Docs()", p)
		}
		return nil
	})
}

var link = regexp.MustCompile(`\]\(([^)#:]+)(#[^)]*)?\)`)

func TestRelativeLinksResolve(t *testing.T) {
	for _, d := range skills.Docs() {
		if d.MIMEType != "text/markdown" {
			continue
		}
		text := skills.MustRead(d.Path)
		for _, m := range link.FindAllStringSubmatch(text, -1) {
			target := path.Join(path.Dir(d.Path), m[1])
			if _, err := skills.Read(target); err != nil {
				t.Errorf("%s links to %s, which does not exist", d.Path, m[1])
			}
		}
	}
}

func TestFrontMatter(t *testing.T) {
	text := skills.MustRead("SKILL.md")
	parts := strings.SplitN(text, "---\n", 3)
	if len(parts) != 3 || parts[0] != "" {
		t.Fatal("SKILL.md must start with --- front matter ---")
	}
	fm := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(parts[1]), "\n") {
		k, v, _ := strings.Cut(line, ": ")
		fm[k] = v
	}
	if fm["name"] != skills.Dir {
		t.Errorf("name %q, want %q (the folder name)", fm["name"], skills.Dir)
	}
	if d := fm["description"]; len(d) < 100 || len(d) > 1024 || !strings.Contains(d, "Use when") {
		t.Errorf("description must be 100-1024 characters and say when to use it: %q", d)
	}
}

func TestTemplatesFollowTheHouseFormat(t *testing.T) {
	for _, name := range []string{"single-part-template", "multi-part-template"} {
		code := skills.Text(name)
		for _, want := range []string{"//  PARTS", "ASSEMBLY ORDER", "PRINT SETTINGS", "TUNING", "/* [Fit] */", "/* [Hidden] */", "assert(", `echo(str("INFO:`, "elephant_foot", "// ----------------------------------------------------------- dispatcher"} {
			if !strings.Contains(code, want) {
				t.Errorf("%s template is missing %q", name, want)
			}
		}
	}
	multi := skills.Text("multi-part-template")
	for _, want := range []string{"assembly = [", `assembly_only = "";`, "module step(id)", `part == "assembled"`, `part == "plate"`, "coupon_", "check_interference", "cube(2, center = true)"} {
		if !strings.Contains(multi, want) {
			t.Errorf("multi-part template is missing %q", want)
		}
	}
}
