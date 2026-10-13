package mcpserver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/margarit/printpipe/internal/assembly"
	"github.com/margarit/printpipe/internal/mcpserver"
	"github.com/margarit/printpipe/internal/mesh"
	"github.com/margarit/printpipe/internal/pipeline/pipelinetest"
	"github.com/margarit/printpipe/skills"
)

func connectWithAssembly(t *testing.T) (*mcp.ClientSession, *pipelinetest.Env) {
	t.Helper()
	env := pipelinetest.New(t)
	asm := assembly.New(env.SCAD, env.Projects, nil)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := mcpserver.New(env.Svc, "test", nil, asm).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, env
}

func TestSkillIsServedAsResources(t *testing.T) {
	cs, _ := connectWithAssembly(t)
	list, err := cs.ListResources(ctx, nil)
	if err != nil || len(list.Resources) != len(skills.Docs()) {
		t.Fatalf("%v: %d resources, want %d", err, len(list.Resources), len(skills.Docs()))
	}
	for _, d := range skills.Docs() {
		r, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: d.URI})
		if err != nil || len(r.Contents) != 1 || r.Contents[0].Text != skills.MustRead(d.Path) || r.Contents[0].MIMEType != d.MIMEType {
			t.Errorf("%s: %v", d.URI, err)
		}
	}
	r, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: mcpserver.AssemblyGuideURI})
	if err != nil {
		t.Fatal(err)
	}
	text := r.Contents[0].Text
	for _, want := range []string{"assembly = [", "assembly_only", "module step(id)", "get_assembly", "build order"} {
		if !strings.Contains(text, want) {
			t.Errorf("guide is missing %q", want)
		}
	}
	if r, _ := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: mcpserver.ModelingGuideURI}); !strings.Contains(r.Contents[0].Text, "Customizer") {
		t.Error("modelling guide")
	}
	if r, _ := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "printpipe://skill"}); !strings.Contains(r.Contents[0].Text, "name: printpipe-cad") {
		t.Error("skill overview")
	}
}

func TestEveryPrompt(t *testing.T) {
	cs, _ := connectWithAssembly(t)
	list, err := cs.ListPrompts(ctx, nil)
	if err != nil || len(list.Prompts) != 7 {
		t.Fatalf("%v %+v", err, list)
	}
	for _, c := range []struct {
		name string
		args map[string]string
		want []string
	}{
		{"design_part", map[string]string{"request": "a pipe clamp", "project": "clamp"}, []string{"a pipe clamp", "project clamp", "printpipe://templates/single-part", "Wall Hook", "printpipe://guides/printability"}},
		{"design_assembly", map[string]string{"request": "a box with a lid"}, []string{"a box with a lid", "get_assembly", "check_interference", "slightly negative", "Snap Box", "printpipe://guides/multi-part"}},
		{"review_model", map[string]string{"project": "upio-orbit"}, []string{`get_model for "upio-orbit"`, "Review checklist", "most serious first"}},
		{"tune_fit", map[string]string{"project": "box", "problem": "lid too tight"}, []string{"The user reports: lid too tight", "coupon_", "Clearances"}},
		{"print_model", map[string]string{"project": "box", "part": "lid"}, []string{`the "lid" option`, "list_printers", "request_approval", "never assembled", "print_job"}},
	} {
		res, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: c.name, Arguments: c.args})
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		text := res.Messages[0].Content.(*mcp.TextContent).Text
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s prompt is missing %q", c.name, w)
			}
		}
		if _, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: c.name}); err == nil {
			t.Errorf("%s accepted no arguments", c.name)
		}
	}
}

// The architect prompt starts a design from nothing: no project, not even a request. Claude
// interviews the user, decides the architecture and names and creates the project itself.
func TestDesignPromptNeedsNoProject(t *testing.T) {
	cs, _ := connectWithAssembly(t)
	list, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var found *mcp.Prompt
	for _, p := range list.Prompts {
		if p.Name == "design" {
			found = p
		}
	}
	if found == nil {
		t.Fatal("no design prompt")
	}
	for _, a := range found.Arguments {
		if a.Required {
			t.Errorf("design prompt requires %q", a.Name)
		}
	}

	res, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "design"})
	if err != nil {
		t.Fatalf("design without arguments: %v", err)
	}
	text := res.Messages[0].Content.(*mcp.TextContent).Text
	for _, want := range []string{
		"architect",
		"Ask the user what they want to make",
		"Name the project yourself",
		"list_projects",
		"creates the project",
		"single part or several",
		"printpipe://templates/single-part",
		"printpipe://templates/multi-part",
		"printpipe://guides/fits",
		"Definition of done",
		"printpipe://guides/printability",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("design prompt is missing %q", want)
		}
	}

	withIdea, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "design", Arguments: map[string]string{"idea": "a desk organiser for pens and an iPad"}})
	if err != nil {
		t.Fatal(err)
	}
	text = withIdea.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(text, "a desk organiser for pens and an iPad") || strings.Contains(text, "Ask the user what they want to make") {
		t.Errorf("idea not used:\n%s", text[:300])
	}
}

func TestAssemblyPrompt(t *testing.T) {
	cs, _ := connectWithAssembly(t)
	list, err := cs.ListPrompts(ctx, nil)
	if err != nil || len(list.Prompts) != 7 {
		t.Fatalf("%v %+v", err, list)
	}
	res, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "add_assembly_timeline", Arguments: map[string]string{"project": "upio-orbit", "order": "cap, ring, sun"}})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Messages[0].Content.(*mcp.TextContent).Text
	for _, want := range []string{`get_model for "upio-orbit"`, "Use this build order from the user: cap, ring, sun", "get_assembly", "assembly_only", "press Play"} {
		if !strings.Contains(text, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	if _, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "add_assembly_timeline", Arguments: map[string]string{}}); err == nil {
		t.Error("prompt without a project accepted")
	}
	design, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "design_part", Arguments: map[string]string{"request": "a pipe clamp"}})
	if err != nil || !strings.Contains(design.Messages[0].Content.(*mcp.TextContent).Text, "a pipe clamp") {
		t.Fatalf("%v", err)
	}
	if _, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "design_part"}); err == nil {
		t.Error("design without a request accepted")
	}
}

func TestGetAssemblyReportsProblems(t *testing.T) {
	cs, _ := connectWithAssembly(t)
	call(t, cs, "compile_model", map[string]any{"project": "box", "scad_code": pipelinetest.Cube})
	res := call(t, cs, "get_assembly", map[string]any{"project": "box"})
	if res.IsError {
		t.Fatal(text(res))
	}
	out := structured[mcpserver.AssemblyOut](t, res)
	if out.Mode != assembly.Pieces || len(out.Parts) != 2 || len(out.Problems) != 1 || !strings.Contains(out.Problems[0], "no assembly steps") {
		t.Fatalf("%+v", out)
	}
	stepped := "part = \"assembled\"; // [assembled, a]\nsize = 20; // [5:300]\n/* [Hidden] */\nassembly = [[\"a\", \"A\"], [\"b\", \"B\"], [\"c\", \"C\"]];\nassembly_only = \"\";\ncube(size);\n"
	compiled := call(t, cs, "compile_model", map[string]any{"project": "box", "scad_code": stepped})
	if !strings.Contains(text(compiled), "Assembly: 3 steps") {
		t.Fatalf("summary: %s", text(compiled))
	}
	out = structured[mcpserver.AssemblyOut](t, call(t, cs, "get_assembly", map[string]any{"project": "box"}))
	// The fake OpenSCAD draws the same two-colour model for every step, so all three share a colour.
	if out.Mode != assembly.Steps || len(out.Parts) != 3 || len(out.Problems) != 1 || !strings.Contains(out.Problems[0], "share the colour") {
		t.Fatalf("%+v", out)
	}
	if res := call(t, cs, "get_assembly", map[string]any{"project": "missing"}); !res.IsError {
		t.Fatal("unknown project")
	}
}

func TestCompileHintsAtAssemblySteps(t *testing.T) {
	cs, _ := connectWithAssembly(t)
	res := call(t, cs, "compile_model", map[string]any{"project": "box", "scad_code": "part = \"assembled\"; // [assembled, base]\nsize = 20;\ncube(size);\n"})
	if !strings.Contains(text(res), "no assembly steps") {
		t.Fatalf("%s", text(res))
	}
}

func TestReview(t *testing.T) {
	r := assembly.Result{Project: "p", Version: 2, Mode: assembly.Steps, Parts: []assembly.Part{
		{ID: "a", Label: "Base", Color: "#FF0000", Count: 1, Max: mesh.Vec{X: 10, Y: 10, Z: 2}},
		{ID: "b", Label: "Pins", Color: "#00FF00", Count: 4, Min: mesh.Vec{Z: 2}, Max: mesh.Vec{X: 1, Y: 1, Z: 8}},
		{ID: "c", Label: "Lid", Error: "step \"c\" renders nothing"},
	}}
	summary, problems := mcpserver.Review(r)
	if !strings.Contains(summary, "2. Pins ×4 (#00FF00)") || !strings.Contains(summary, "3. Lid: FAILED") || len(problems) != 1 {
		t.Fatalf("%s %v", summary, problems)
	}
	r.Parts = r.Parts[:2]
	if summary, problems := mcpserver.Review(r); len(problems) != 0 || !strings.Contains(summary, "press Play") {
		t.Fatalf("%s %v", summary, problems)
	}
	_ = context.Background
}
