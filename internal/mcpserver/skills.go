package mcpserver

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/margarit/printpipe/internal/assembly"
	"github.com/margarit/printpipe/internal/pipeline"
)

// Guides are served as MCP resources, and the prompts hand them to the agent.
var (
	//go:embed guides/assembly.md
	assemblyGuide string
	//go:embed guides/modeling.md
	modelingGuide string
)

const (
	AssemblyGuideURI = "printpipe://guides/assembly"
	ModelingGuideURI = "printpipe://guides/modeling"
)

func addGuides(s *mcp.Server) {
	for _, g := range []struct{ uri, name, title, desc, text string }{
		{AssemblyGuideURI, "assembly-guide", "Assembly timeline guide", "How to declare a model's build order so the viewer can play its parts into place one by one.", assemblyGuide},
		{ModelingGuideURI, "modeling-guide", "Modelling guide", "Customizer parameters, part options and printability rules for this pipeline.", modelingGuide},
	} {
		text := g.text
		s.AddResource(&mcp.Resource{URI: g.uri, Name: g.name, Title: g.title, Description: g.desc, MIMEType: "text/markdown"},
			func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/markdown", Text: text}}}, nil
			})
	}
}

// AssemblyPrompt is the step-by-step task the add_assembly_timeline prompt gives the agent.
func AssemblyPrompt(project, order string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Add an assembly timeline to the print pipeline project %q, so the viewer's Assembly tab plays its parts into place in build order.\n\n", project)
	b.WriteString("Do this:\n")
	fmt.Fprintf(&b, "1. Call get_model for %q and read the code: find every part and how the assembled view places it.\n", project)
	if order != "" {
		fmt.Fprintf(&b, "2. Use this build order from the user: %s.\n", order)
	} else {
		b.WriteString("2. Work out the physical build order: what goes in first, and what each later part goes into or onto. Put copies inserted together (several identical gears, screws) in one step.\n")
	}
	b.WriteString("3. Add the `assembly` declaration, `assembly_only` and the `step()` wrappers as the guide below shows. Give every step its own color(). Keep or add an \"assembled\" option on the `part` parameter. Don't change any part's geometry or position.\n")
	fmt.Fprintf(&b, "4. Compile the full new code with compile_model (project %q). The summary must say \"Assembly: N steps\".\n", project)
	fmt.Fprintf(&b, "5. Call get_assembly for %q. Every step must render, with the counts you expect. Fix and recompile until it reports no problems.\n", project)
	b.WriteString("6. Tell the user the build order you chose, and to open the Assembly tab in the viewer and press Play.\n\n")
	b.WriteString("---\n\n")
	b.WriteString(assemblyGuide)
	return b.String()
}

func addPrompts(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        "add_assembly_timeline",
		Title:       "Add an assembly timeline",
		Description: "Declare a model's build order so the viewer's Assembly tab plays its parts into place one by one, then check it with get_assembly.",
		Arguments: []*mcp.PromptArgument{
			{Name: "project", Description: "The project to add the timeline to", Required: true},
			{Name: "order", Description: "Optional build order in your own words, e.g. \"bottom cap, ring, carrier, sun, planets, cover, top cap\""},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		project := strings.TrimSpace(req.Params.Arguments["project"])
		if project == "" {
			return nil, fmt.Errorf("the project argument is required")
		}
		return &mcp.GetPromptResult{
			Description: "Add an assembly timeline to " + project,
			Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{
				Text: AssemblyPrompt(project, strings.TrimSpace(req.Params.Arguments["order"])),
			}}},
		}, nil
	})
	s.AddPrompt(&mcp.Prompt{
		Name:        "design_part",
		Title:       "Design a printable part",
		Description: "Design a part the pipeline can print: Customizer parameters, preview check, estimate, then approval.",
		Arguments: []*mcp.PromptArgument{
			{Name: "request", Description: "What to make, e.g. \"a wall bracket for a 25 mm pipe\"", Required: true},
			{Name: "project", Description: "Project name (letters, digits, - and _)"},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		request := strings.TrimSpace(req.Params.Arguments["request"])
		if request == "" {
			return nil, fmt.Errorf("the request argument is required")
		}
		project := strings.TrimSpace(req.Params.Arguments["project"])
		if project == "" {
			project = "a short descriptive name"
		}
		text := fmt.Sprintf("Design this part for the print pipeline: %s\n\n"+
			"1. Write OpenSCAD with named Customizer parameters (guide below) and compile it with compile_model, project %s.\n"+
			"2. Look at the preview images; fix wrong geometry and recompile.\n"+
			"3. If it has several parts, add an assembly timeline (%s).\n"+
			"4. Call estimate_print and tell the user the time, filament and cost.\n"+
			"5. When the user wants it printed, call request_approval, ask them to click Approve in the viewer, then call print_job.\n\n---\n\n%s",
			request, project, AssemblyGuideURI, modelingGuide)
		return &mcp.GetPromptResult{
			Description: "Design " + request,
			Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
		}, nil
	})
}

type AssemblyIn struct {
	Project string `json:"project"`
	Version int    `json:"version,omitempty" jsonschema:"version; latest if empty"`
}

type AssemblyOut struct {
	assembly.Result
	Problems []string `json:"problems"`
	Summary  string   `json:"summary"`
}

// Review lists what the agent should fix in an assembly, and a one-line summary per step.
func Review(r assembly.Result) (summary string, problems []string) {
	var b strings.Builder
	switch r.Mode {
	case assembly.Steps:
		fmt.Fprintf(&b, "Assembly of %s v%d: %d steps from the model's declaration.\n", r.Project, r.Version, len(r.Parts))
	case assembly.Pieces:
		fmt.Fprintf(&b, "Assembly of %s v%d: no declaration, so the assembled model was split into %d unnamed pieces.\n", r.Project, r.Version, len(r.Parts))
		problems = append(problems, "The model declares no assembly steps: add the assembly declaration ("+AssemblyGuideURI+") for a named, ordered timeline.")
	}
	for i, p := range r.Parts {
		count := ""
		if p.Count > 1 {
			count = fmt.Sprintf(" ×%d", p.Count)
		}
		if p.Error != "" {
			fmt.Fprintf(&b, "%d. %s%s: FAILED: %s\n", i+1, p.Label, count, p.Error)
			problems = append(problems, fmt.Sprintf("Step %q failed: %s", p.ID, p.Error))
			continue
		}
		fmt.Fprintf(&b, "%d. %s%s (%s): %.1f × %.1f × %.1f mm at z %.1f–%.1f\n", i+1, p.Label, count, p.Color,
			p.Max.X-p.Min.X, p.Max.Y-p.Min.Y, p.Max.Z-p.Min.Z, p.Min.Z, p.Max.Z)
	}
	colors := map[string][]string{}
	for _, p := range r.Parts {
		if p.Error == "" && p.Color != "" {
			colors[p.Color] = append(colors[p.Color], p.Label)
		}
	}
	if r.Mode == assembly.Steps {
		for c, labels := range colors {
			if len(labels) > 2 {
				problems = append(problems, fmt.Sprintf("%d steps share the colour %s (%s): give them their own colours so the user can tell them apart.", len(labels), c, strings.Join(labels, ", ")))
			}
		}
	}
	if len(problems) == 0 {
		b.WriteString("No problems. Ask the user to open the Assembly tab in the viewer and press Play.")
	}
	return strings.TrimSpace(b.String()), problems
}

func addAssemblyTool(s *mcp.Server, svc *pipeline.Service, asm *assembly.Service) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_assembly",
		Description: "Build and check a version's assembly timeline (the viewer's Assembly tab): its steps in build order, " +
			"instance counts, colours, positions and any step that failed. Use it after adding assembly steps (see " + AssemblyGuideURI + ").",
		Annotations: readOnly("Get assembly"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in AssemblyIn) (*mcp.CallToolResult, AssemblyOut, error) {
		v, err := svc.Version(in.Project, in.Version)
		if err != nil {
			return nil, AssemblyOut{}, err
		}
		r, err := asm.Get(ctx, v)
		if err != nil {
			return nil, AssemblyOut{}, err
		}
		if r.Status == "failed" {
			return nil, AssemblyOut{}, fmt.Errorf("the assembly could not be built: %s", r.Error)
		}
		summary, problems := Review(r)
		if problems == nil {
			problems = []string{}
		}
		return nil, AssemblyOut{Result: r, Problems: problems, Summary: summary}, nil
	})
}
