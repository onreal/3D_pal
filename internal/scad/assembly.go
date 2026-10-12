package scad

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Step is one part of an assembly, in build order.
type Step struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

var assemblyRe = regexp.MustCompile(`(?m)^\s*assembly\s*=\s*\[`)

// AssemblyVar is the variable the pipeline sets to render one assembly step on its own.
const AssemblyVar = "assembly_only"

// ParseAssembly reads the model's assembly declaration, written at the top level as
//
//	assembly = [["cap_bottom", "Bottom cap"], ["ring", "Ring"], "sun"];
//
// Each item is an ID, or an [ID, label] pair, in build order. The model must also have
// `assembly_only = "";` and draw only that step's part when it is set.
func ParseAssembly(src string) ([]Step, error) {
	loc := assemblyRe.FindStringIndex(src)
	if loc == nil {
		return nil, nil
	}
	if !regexp.MustCompile(`(?m)^\s*` + AssemblyVar + `\s*=`).MatchString(src) {
		return nil, fmt.Errorf("the model declares assembly steps but no %s variable; add `%s = \"\";` and draw only the matching part when it is set", AssemblyVar, AssemblyVar)
	}
	// Find the matching closing bracket, skipping strings.
	depth, inStr, end := 0, false, -1
	for i := loc[1] - 1; i < len(src); i++ {
		c := src[i]
		switch {
		case inStr && c == '\\':
			i++
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '/' && i+1 < len(src) && src[i+1] == '/': // skip a line comment
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*': // skip a block comment
			if j := strings.Index(src[i+2:], "*/"); j >= 0 {
				i += j + 3
			} else {
				i = len(src)
			}
		case c == '[':
			depth++
		case c == ']':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return nil, errors.New("assembly = [ … has no closing ]")
	}
	body := regexp.MustCompile(`(?s)//[^\n]*|/\*.*?\*/`).ReplaceAllString(src[loc[1]-1:end+1], "")
	var steps []Step
	seen := map[string]bool{}
	items := regexp.MustCompile(`\[\s*("(?:\\.|[^"\\])*")\s*,\s*("(?:\\.|[^"\\])*")\s*\]|("(?:\\.|[^"\\])*")`).FindAllStringSubmatch(body, -1)
	for _, m := range items {
		var id, label string
		if m[1] != "" {
			id, _ = strconv.Unquote(m[1])
			label, _ = strconv.Unquote(m[2])
		} else {
			id, _ = strconv.Unquote(m[3])
			label = id
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		steps = append(steps, Step{ID: id, Label: label})
	}
	if len(steps) == 0 {
		return nil, errors.New("assembly = [] lists no steps")
	}
	return steps, nil
}

// AssembledOption is the value of a `part` parameter that shows the whole assembly, if any.
func AssembledOption(params []Param) (string, bool) {
	for _, p := range params {
		if p.Name != "part" {
			continue
		}
		for _, want := range []string{"assembled", "assembly", "assemble", "all_assembled"} {
			for _, o := range p.Options {
				if s, ok := o.Value.(string); ok && strings.EqualFold(s, want) {
					return s, true
				}
			}
		}
	}
	return "", false
}

// Export3MF writes the model as 3MF, which keeps each part's colour.
func (c *Compiler) Export3MF(ctx context.Context, scadPath, out string, defines []string) error {
	os.Remove(out)
	args := []string{"-o", out}
	if c.backend != "" {
		args = append(args, "--backend="+c.backend)
	}
	args = append(args, defines...)
	args = append(args, scadPath)
	if _, err := c.run.Run(ctx, c.binary, args...); err != nil {
		return fmt.Errorf("OpenSCAD could not render the assembly:\n%s", cleanError(err.Error()))
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		return errors.New("OpenSCAD produced no geometry for this assembly step")
	}
	return nil
}
