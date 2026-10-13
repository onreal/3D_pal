package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/margarit/printpipe/internal/assembly"
	"github.com/margarit/printpipe/internal/pipeline"
	"github.com/margarit/printpipe/skills"
)

// The printpipe-cad skill (package skills) is served as MCP resources, and the prompts
// hand the right parts of it to the agent for each task.
var (
	AssemblyGuideURI = skills.GuideURI("assembly")
	ModelingGuideURI = skills.GuideURI("modeling")
)

func addGuides(s *mcp.Server) {
	for _, d := range skills.Docs() {
		text, mime := skills.MustRead(d.Path), d.MIMEType
		s.AddResource(&mcp.Resource{URI: d.URI, Name: d.Name, Title: d.Title, Description: d.Description, MIMEType: mime},
			func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: mime, Text: text}}}, nil
			})
	}
}

// attach appends documents of the skill to a prompt, each under its resource URI.
func attach(b *strings.Builder, names ...string) {
	for _, n := range names {
		d, _ := skills.Find(n)
		fmt.Fprintf(b, "\n\n---\n\n<!-- %s -->\n", d.URI)
		if d.MIMEType != "text/markdown" {
			fmt.Fprintf(b, "```openscad\n%s\n```", strings.TrimSpace(skills.MustRead(d.Path)))
			continue
		}
		b.WriteString(strings.TrimSpace(skills.MustRead(d.Path)))
	}
}

func orName(project, fallback string) string {
	if project == "" {
		return fallback
	}
	return project
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
	b.WriteString("6. Tell the user the build order you chose, and to open the Assembly tab in the viewer and press Play.")
	attach(&b, "assembly")
	return b.String()
}

// ArchitectPrompt is the task for designing something from an idea, or from nothing at all.
// Unlike every other prompt it takes no project: the agent works out the design, names the
// project and creates it with its first compile_model call. idea may be empty, in which case
// the agent opens by asking what to make.
func ArchitectPrompt(idea string) string {
	var b strings.Builder
	b.WriteString("You are the CAD and 3D-printing architect for this design. The user has no project for it: you decide the design, name the project and create it.\n\n")
	if idea != "" {
		fmt.Fprintf(&b, "The idea: %s\n\n", idea)
	} else {
		b.WriteString("There is no idea yet. Ask the user what they want to make, what it is for and what it has to fit or hold, in one short message, then continue with the steps below.\n\n")
	}
	b.WriteString("1. Brief. Work out what the object does, what it attaches to, holds or fits around, the loads and environment, the material and how many are needed. Ask only for what you would otherwise have to guess, above all the real measurements of anything it mates with. Choose sensible defaults for the rest and say what you chose.\n")
	b.WriteString("2. Architecture. Decide whether it is a single part or several: split it when it is bigger than the printer (list_printers gives the build volume), when parts move against each other, when it must assemble around something, or when one piece would need supports a split avoids. For each part choose the print orientation (biggest flat face down, loads along the layers, no overhang over 45 degrees) and how the parts join (screws, snaps, press fits, hinges, with named clearances from " + skills.GuideURI("fits") + "). Share the brief in a few lines: parts, orientation, joints, key dimensions, material. Then carry on; stop only if a measurement you need is missing.\n")
	b.WriteString("3. Name the project yourself. Use a short descriptive name of lowercase words joined by -, for example pipe-clamp-25mm (letters, digits, - and _, starting with a letter or digit, at most 64 characters). Call list_projects first: if the name is taken by something else, add -2, -3 and so on. Never write into an existing project unless the user asked you to change that project.\n")
	b.WriteString("4. Your first compile_model call with that name creates the project. Write the model in the house format from the template: " + skills.TemplateURI("single-part") + " for a single part, " + skills.TemplateURI("multi-part") + " for several parts (read " + skills.GuideURI("multi-part") + " and " + skills.GuideURI("assembly") + " first).\n")
	b.WriteString("5. Read every compile summary (size, fits line, INFO and CHECK lines, warnings) and look at the previews. Fix and recompile until it matches the brief with no warnings. For several parts, also compile each part, compile assembled and run get_assembly, and compile check_interference.\n")
	b.WriteString("6. Estimate with estimate_print. Then tell the user the project name, the parts and how each prints, the time, filament and cost, and which coupon to print first if a fit matters. Offer to print, but only request approval when they ask: you cannot approve a print yourself.")
	attach(&b, "skill", "modeling", "printability")
	return b.String()
}

// DesignPartPrompt is the task for a new single-part model.
func DesignPartPrompt(request, project string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Design this part for the print pipeline: %s\n\n", request)
	b.WriteString("Follow the printpipe-cad house format (overview, modelling and printability guides and the single-part template below).\n\n")
	b.WriteString("1. Pin down the dimensions it must fit, the material and the printer (list_printers gives the build volume). Ask the user for any measurement you would otherwise guess.\n")
	b.WriteString("2. Choose the print orientation first: biggest flat face down, loads along the layers, no overhang over 45 degrees.\n")
	fmt.Fprintf(&b, "3. Write the model from the template, in the house format, and compile it with compile_model (project %s).\n", orName(project, "a short descriptive name"))
	b.WriteString("4. Read the summary (size, fit, INFO lines, warnings) and look at the previews; fix and recompile until it is right and has no warnings.\n")
	fmt.Fprintf(&b, "5. If it has to fit something, add a coupon_* option (%s).\n", skills.GuideURI("fits"))
	b.WriteString("6. Call estimate_print and tell the user the time, filament and cost. Print only if they ask (print_model prompt, or the tools guide).")
	attach(&b, "skill", "modeling", "printability", "single-part-template")
	return b.String()
}

// DesignAssemblyPrompt is the task for a new object with several printed parts.
func DesignAssemblyPrompt(request, project string) string {
	var b strings.Builder
	p := orName(project, "a short descriptive name")
	fmt.Fprintf(&b, "Design this multi-part object for the print pipeline: %s\n\n", request)
	fmt.Fprintf(&b, "Follow the printpipe-cad house format. Read %s and %s for mating features and %s for the checks.\n\n", skills.GuideURI("fits"), skills.GuideURI("printability"), skills.GuideURI("validation"))
	b.WriteString("1. List the parts, how each one prints, and how they join (multi-part guide). Ask the user for any dimension or hardware you would otherwise guess.\n")
	b.WriteString("2. Write one file from the multi-part template: a module per part in print orientation on z = 0, shared interface dimensions with named clearances, placements, assembled/exploded/section, plate, coupon_* and check_interference options, and the assembly declaration.\n")
	fmt.Fprintf(&b, "3. Compile it with compile_model (project %s), then compile each printable part with params {\"part\": ...} and look at every preview. Fix every warning.\n", p)
	b.WriteString("4. Compile assembled and call get_assembly until it reports no problems.\n")
	b.WriteString("5. Compile check_interference: clean is Size 2.0 x 2.0 x 2.0 mm. For moving parts check several positions. Then compile it once with a clearance set slightly negative and confirm the clash appears.\n")
	b.WriteString("6. Estimate the plate or each part, and tell the user the parts list, the build order, which coupon to print first, and the time and cost.")
	attach(&b, "skill", "multi-part", "assembly", "multi-part-template")
	return b.String()
}

// ReviewPrompt is the task for auditing an existing project against the house format.
func ReviewPrompt(project string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review the print pipeline project %q against the printpipe-cad house format and quality bar.\n\n", project)
	fmt.Fprintf(&b, "1. Call get_model for %q and read the code and parameters.\n", project)
	b.WriteString("2. Compile each part option with params only (empty scad_code) and look at the summaries and previews. For assemblies also run get_assembly and check_interference.\n")
	b.WriteString("3. Go through the review checklist below item by item.\n")
	b.WriteString("4. Report what you found, most serious first: what is wrong, why it matters for the print, and the fix. Change the model only after the user agrees, then recompile and check again.")
	attach(&b, "validation", "printability", "modeling")
	return b.String()
}

// TuneFitPrompt is the task for adjusting a fit after a test print.
func TuneFitPrompt(project, problem string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Tune a fit in the print pipeline project %q.", project)
	if problem != "" {
		fmt.Fprintf(&b, " The user reports: %s", problem)
	}
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "1. Call get_model for %q. Find the [Fit] parameters for the parts involved.\n", project)
	b.WriteString("2. Map the problem to a parameter and a change with the tables below (usually 0.05 mm per side per step). Ask the user if the symptom is unclear.\n")
	b.WriteString("3. Recompile with params only (empty scad_code). If the model has a coupon_* option for this fit, compile that coupon so the user can test before reprinting the part.\n")
	b.WriteString("4. Tell the user what you changed, from what to what, and what to print next.")
	attach(&b, "fits", "troubleshooting")
	return b.String()
}

// PrintPrompt is the task for printing a part of a project.
func PrintPrompt(project, part string) string {
	var b strings.Builder
	what := "the part the user wants"
	if part != "" {
		what = fmt.Sprintf("the %q option", part)
	}
	fmt.Fprintf(&b, "Print %s of the print pipeline project %q.\n\n", what, project)
	b.WriteString("1. Call list_printers. With no printer configured, stop and ask the user to add one in the viewer.\n")
	fmt.Fprintf(&b, "2. Call get_model for %q. Pick a printable option: a part, plate or coupon, never assembled, exploded, section or check_*.\n", project)
	b.WriteString("3. Compile that option as its own version with params only, unless the latest version already is it. The summary must say it fits the printer.\n")
	b.WriteString("4. Call estimate_print and tell the user the time, filament and cost. Use slice_model with supports off if you need to see whether it needs supports.\n")
	b.WriteString("5. Call request_approval for that version and ask the user to review it in the viewer and click Approve. Wait for them.\n")
	b.WriteString("6. Call print_job with the job_id. Only then say it is printing. Use printer_status when asked how it is going; cancel_print only if the user asks to stop.")
	attach(&b, "tools")
	return b.String()
}

type promptArg struct {
	name, desc string
	required   bool
}

func addPrompt(s *mcp.Server, name, title, desc string, args []promptArg, build func(map[string]string) (string, string)) {
	p := &mcp.Prompt{Name: name, Title: title, Description: desc}
	for _, a := range args {
		p.Arguments = append(p.Arguments, &mcp.PromptArgument{Name: a.name, Description: a.desc, Required: a.required})
	}
	s.AddPrompt(p, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		in := map[string]string{}
		for _, a := range args {
			in[a.name] = strings.TrimSpace(req.Params.Arguments[a.name])
			if a.required && in[a.name] == "" {
				return nil, fmt.Errorf("the %s argument is required", a.name)
			}
		}
		summary, text := build(in)
		return &mcp.GetPromptResult{
			Description: summary,
			Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
		}, nil
	})
}

func addPrompts(s *mcp.Server) {
	project := promptArg{"project", "The project name (letters, digits, - and _)", true}
	newProject := promptArg{"project", "Project name for the new model (letters, digits, - and _)", false}
	request := promptArg{"request", "What to make, e.g. \"a wall bracket for a 25 mm pipe\"", true}
	// The way in when the user has nothing yet: no project, not even a request is required.
	addPrompt(s, "design", "Design something (architect mode)",
		"Start from an idea, or from nothing: Claude acts as the CAD and 3D-printing architect, asks what it needs, decides single part or assembly, names and creates the project itself, and takes it to an estimate.",
		[]promptArg{{"idea", "Optional: what you want to make, in your own words, e.g. \"a wall mount for my router\"", false}},
		func(in map[string]string) (string, string) {
			if in["idea"] == "" {
				return "Design something new", ArchitectPrompt("")
			}
			return "Design " + in["idea"], ArchitectPrompt(in["idea"])
		})
	addPrompt(s, "design_part", "Design a printable part",
		"Design a single printable part in the house format: Customizer parameters, checks, preview review and an estimate.",
		[]promptArg{request, newProject}, func(in map[string]string) (string, string) {
			return "Design " + in["request"], DesignPartPrompt(in["request"], in["project"])
		})
	addPrompt(s, "design_assembly", "Design a multi-part object",
		"Design an object made of several printed parts: part options, interfaces with named clearances, assembly timeline, interference check, coupons and plate.",
		[]promptArg{request, newProject}, func(in map[string]string) (string, string) {
			return "Design " + in["request"], DesignAssemblyPrompt(in["request"], in["project"])
		})
	addPrompt(s, "add_assembly_timeline", "Add an assembly timeline",
		"Declare a model's build order so the viewer's Assembly tab plays its parts into place one by one, then check it with get_assembly.",
		[]promptArg{{"project", "The project to add the timeline to", true}, {"order", "Optional build order in your own words, e.g. \"bottom cap, ring, carrier, sun, planets, cover, top cap\"", false}},
		func(in map[string]string) (string, string) {
			return "Add an assembly timeline to " + in["project"], AssemblyPrompt(in["project"], in["order"])
		})
	addPrompt(s, "review_model", "Review a model",
		"Audit an existing project against the house format and quality bar, and report problems with fixes.",
		[]promptArg{project}, func(in map[string]string) (string, string) {
			return "Review " + in["project"], ReviewPrompt(in["project"])
		})
	addPrompt(s, "tune_fit", "Tune a fit",
		"Adjust a model's clearances after a test print (too tight, too loose, snaps, gears), and compile the coupon to test it.",
		[]promptArg{project, {"problem", "What happened, e.g. \"the lid is too tight\"", false}}, func(in map[string]string) (string, string) {
			return "Tune a fit in " + in["project"], TuneFitPrompt(in["project"], in["problem"])
		})
	addPrompt(s, "print_model", "Print a model",
		"Print a part, plate or coupon: pick the printable option, check it fits, estimate, request approval, then start the job once the user approves.",
		[]promptArg{project, {"part", "Optional part, plate or coupon option to print", false}}, func(in map[string]string) (string, string) {
			return "Print " + in["project"], PrintPrompt(in["project"], in["part"])
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
