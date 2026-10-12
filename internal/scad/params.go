package scad

import (
	"bufio"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Param is one top-level parameter written in OpenSCAD Customizer style, such as
//
//	wall = 2; // [1:0.5:5] Wall thickness in mm
type Param struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"` // "number", "boolean", "string", "vector"
	Value   any      `json:"value"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	Step    *float64 `json:"step,omitempty"`
	Options []Option `json:"options,omitempty"`
	Label   string   `json:"label,omitempty"`
	Group   string   `json:"group,omitempty"`
}

type Option struct {
	Value any    `json:"value"`
	Label string `json:"label"`
}

var (
	assignRe = regexp.MustCompile(`^\s*([A-Za-z_$][A-Za-z0-9_]*)\s*=\s*(.+?)\s*;\s*(?://\s*(.*))?$`)
	groupRe  = regexp.MustCompile(`^\s*/\*\s*\[\s*(.+?)\s*\]\s*\*/\s*$`)
	defRe    = regexp.MustCompile(`^\s*(module|function)\b`)
	numberRe = regexp.MustCompile(`^-?(\d+\.?\d*|\.\d+)([eE][-+]?\d+)?$`)
)

// ParseParams reads the parameters the Customizer would show: literal top-level
// assignments before the first module or function, outside the Hidden group.
func ParseParams(src string) []Param {
	var out []Param
	group, prevComment := "", ""
	depth := 0
	sc := bufio.NewScanner(strings.NewReader(src))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if depth == 0 {
			if defRe.MatchString(line) {
				break
			}
			if m := groupRe.FindStringSubmatch(line); m != nil {
				group = m[1]
				prevComment = ""
				continue
			}
			if strings.HasPrefix(trimmed, "//") {
				prevComment = strings.TrimSpace(strings.TrimPrefix(trimmed, "//"))
				continue
			}
			if m := assignRe.FindStringSubmatch(line); m != nil && !strings.EqualFold(group, "Hidden") {
				if p, ok := parseAssignment(m[1], m[2], m[3]); ok {
					if p.Label == "" {
						p.Label = prevComment
					}
					if group != "" && !strings.EqualFold(group, "Global") {
						p.Group = group
					}
					out = append(out, p)
				}
			}
		}
		depth += strings.Count(stripStrings(line), "{") - strings.Count(stripStrings(line), "}")
		if depth < 0 {
			depth = 0
		}
		if trimmed != "" {
			prevComment = ""
		}
	}
	return out
}

func stripStrings(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		line = line[:i]
	}
	return regexp.MustCompile(`"(?:\\.|[^"\\])*"`).ReplaceAllString(line, `""`)
}

func parseAssignment(name, raw, comment string) (Param, bool) {
	p := Param{Name: name}
	v, typ, ok := literal(raw)
	if !ok {
		return p, false // an expression: the Customizer doesn't show it either
	}
	p.Value, p.Type = v, typ
	comment = strings.TrimSpace(comment)
	if strings.HasPrefix(comment, "[") {
		end := strings.Index(comment, "]")
		if end > 0 {
			annotate(&p, comment[1:end])
			comment = strings.TrimSpace(comment[end+1:])
		}
	}
	p.Label = comment
	return p, true
}

func literal(raw string) (any, string, bool) {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "true" || raw == "false":
		return raw == "true", "boolean", true
	case numberRe.MatchString(raw):
		f, err := strconv.ParseFloat(raw, 64)
		return f, "number", err == nil
	case len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"':
		s, err := strconv.Unquote(raw)
		if err != nil {
			return raw[1 : len(raw)-1], "string", true
		}
		return s, "string", true
	case strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") && !strings.Contains(raw, ":"):
		var vec []float64
		for _, part := range strings.Split(raw[1:len(raw)-1], ",") {
			f, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
			if err != nil {
				return nil, "", false
			}
			vec = append(vec, f)
		}
		return vec, "vector", len(vec) > 0
	}
	return nil, "", false
}

// annotate applies a Customizer bracket: [max], [min:max], [min:step:max], [a, b, c] or [v:Label, ...].
func annotate(p *Param, body string) {
	body = strings.TrimSpace(body)
	if !strings.Contains(body, ",") && p.Type == "number" {
		parts := strings.Split(body, ":")
		nums := make([]float64, 0, 3)
		for _, s := range parts {
			f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
			if err != nil {
				nums = nil
				break
			}
			nums = append(nums, f)
		}
		switch len(nums) {
		case 1:
			zero := 0.0
			p.Min, p.Max = &zero, &nums[0]
			return
		case 2:
			p.Min, p.Max = &nums[0], &nums[1]
			return
		case 3:
			p.Min, p.Step, p.Max = &nums[0], &nums[1], &nums[2]
			return
		}
	}
	if p.Type != "number" && p.Type != "string" {
		return
	}
	for _, item := range strings.Split(body, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		val, label, hasLabel := strings.Cut(item, ":")
		val = strings.TrimSpace(val)
		if !hasLabel {
			label = val
		}
		var v any = strings.Trim(val, `"`)
		if p.Type == "number" {
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				continue
			}
			v = f
		}
		p.Options = append(p.Options, Option{Value: v, Label: strings.TrimSpace(label)})
	}
}

// Define formats an override for openscad -D name=value, checking it against the parameter's type.
func Define(p Param, v any) (string, error) {
	switch p.Type {
	case "number":
		f, ok := toFloat(v)
		if !ok {
			return "", fmt.Errorf("parameter %s must be a number, got %v", p.Name, v)
		}
		return p.Name + "=" + strconv.FormatFloat(f, 'g', -1, 64), nil
	case "boolean":
		b, ok := v.(bool)
		if !ok {
			if s, isStr := v.(string); isStr && (s == "true" || s == "false") {
				b, ok = s == "true", true
			}
		}
		if !ok {
			return "", fmt.Errorf("parameter %s must be true or false, got %v", p.Name, v)
		}
		return p.Name + "=" + strconv.FormatBool(b), nil
	case "string":
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("parameter %s must be text, got %v", p.Name, v)
		}
		return p.Name + "=" + strconv.Quote(s), nil
	case "vector":
		list, ok := v.([]any)
		if !ok {
			if fl, isF := v.([]float64); isF {
				for _, f := range fl {
					list = append(list, f)
				}
				ok = true
			}
		}
		if !ok {
			return "", fmt.Errorf("parameter %s must be a list of numbers, got %v", p.Name, v)
		}
		parts := make([]string, len(list))
		for i, x := range list {
			f, ok := toFloat(x)
			if !ok {
				return "", fmt.Errorf("parameter %s must be a list of numbers, got %v", p.Name, v)
			}
			parts[i] = strconv.FormatFloat(f, 'g', -1, 64)
		}
		return p.Name + "=[" + strings.Join(parts, ",") + "]", nil
	}
	return "", fmt.Errorf("parameter %s has an unsupported type", p.Name)
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

// Defines turns overrides into -D arguments, rejecting names the model doesn't declare.
func Defines(params []Param, overrides map[string]any) ([]string, error) {
	byName := map[string]Param{}
	var names []string
	for _, p := range params {
		byName[p.Name] = p
		names = append(names, p.Name)
	}
	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var args []string
	for _, k := range keys {
		p, ok := byName[k]
		if !ok {
			return nil, fmt.Errorf("unknown parameter %q; the model's top-level parameters are: %s", k, strings.Join(names, ", "))
		}
		d, err := Define(p, overrides[k])
		if err != nil {
			return nil, err
		}
		args = append(args, "-D", d)
	}
	return args, nil
}

// Values returns each parameter's value with overrides applied, for params.json.
func Values(params []Param, overrides map[string]any) map[string]any {
	out := map[string]any{}
	for _, p := range params {
		out[p.Name] = p.Value
		if v, ok := overrides[p.Name]; ok {
			out[p.Name] = v
		}
	}
	return out
}
