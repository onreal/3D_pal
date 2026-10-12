// Package skills embeds the printpipe-cad agent skill: the house format, guides and
// templates for designing printable OpenSCAD models with this pipeline. The same files
// are a Claude Code skill (skills/printpipe-cad, installed by `printpipe.sh skills`) and
// the MCP server's resources and prompts, so every agent gets one set of rules.
package skills

import (
	"embed"
	"io/fs"
	"path"
)

//go:embed printpipe-cad
var files embed.FS

// Dir is the skill's folder name, and its name in the SKILL.md front matter.
const Dir = "printpipe-cad"

// Doc is one file of the skill, served as an MCP resource.
type Doc struct {
	Name        string // unique short name: "skill", "modeling", "multi-part-template"
	Path        string // path inside the skill folder
	URI         string
	Title       string
	Description string
	MIMEType    string
}

const (
	SkillURI    = "printpipe://skill"
	guidePrefix = "printpipe://guides/"
	tmplPrefix  = "printpipe://templates/"
)

// GuideURI is the resource URI of a reference guide, e.g. GuideURI("assembly").
func GuideURI(name string) string { return guidePrefix + name }

// TemplateURI is the resource URI of a template, e.g. TemplateURI("multi-part").
func TemplateURI(name string) string { return tmplPrefix + name }

// Docs lists every file of the skill in reading order: the overview, the guides, then
// the templates.
func Docs() []Doc {
	g := func(name, title, desc string) Doc {
		return Doc{name, "references/" + name + ".md", GuideURI(name), title, desc, "text/markdown"}
	}
	t := func(name, file, title, desc string) Doc {
		return Doc{name + "-template", "templates/" + file, TemplateURI(name), title, desc, "text/plain"}
	}
	return []Doc{
		{"skill", "SKILL.md", SkillURI, "Print pipeline CAD skill", "Start here: the workflow, the house format, the definition of done and which guide to read for each task.", "text/markdown"},
		g("modeling", "Modelling guide", "The house format: header, Customizer parameters, hidden section, checks, part modules and the part dispatcher."),
		g("multi-part", "Multi-part guide", "Splitting objects into parts, print orientation, interfaces and joints, placements, plates and the bill of materials."),
		g("assembly", "Assembly timeline guide", "How to declare a model's build order so the viewer can play its parts into place one by one."),
		g("fits", "Fits and mechanisms guide", "Clearances, hardware hole sizes, snap fits, detents, gears, hinges, threads and test coupons."),
		g("printability", "Printability guide", "FDM rules for walls, overhangs, bridges, the first layer, strength, holes, text and materials."),
		g("validation", "Validation guide", "Asserts, echo output, preview checks, interference checks, coupons and the review checklist."),
		g("tools", "Tools guide", "Every print pipeline tool: what it does, when to use it and the mistakes to avoid."),
		g("troubleshooting", "Troubleshooting guide", "Compile errors, slow compiles, wrong previews, and print problems mapped to parameters."),
		t("single-part", "single_part.scad", "Single-part template", "A complete one-part model in the house format: a screw-on wall hook printed on its side, with a screw coupon."),
		t("multi-part", "multi_part.scad", "Multi-part template", "A complete multi-part model in the house format: a snap box with lid and dividers, assembly timeline, plate, coupon and interference check."),
	}
}

// Read returns a file of the skill by its path inside the skill folder.
func Read(p string) (string, error) {
	b, err := fs.ReadFile(files, path.Join(Dir, p))
	return string(b), err
}

// MustRead is Read for files the package itself lists; it panics if one is missing,
// which a test catches.
func MustRead(p string) string {
	s, err := Read(p)
	if err != nil {
		panic(err)
	}
	return s
}

// Find returns the document with this short name.
func Find(name string) (Doc, bool) {
	for _, d := range Docs() {
		if d.Name == name {
			return d, true
		}
	}
	return Doc{}, false
}

// Text is the content of the document with this short name; it panics on an unknown name.
func Text(name string) string {
	d, ok := Find(name)
	if !ok {
		panic("skills: no document " + name)
	}
	return MustRead(d.Path)
}

// FS is the skill folder, for installing it as a Claude Code skill.
func FS() fs.FS {
	sub, err := fs.Sub(files, Dir)
	if err != nil {
		panic(err)
	}
	return sub
}
