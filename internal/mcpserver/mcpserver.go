// Package mcpserver exposes the pipeline to the agent as MCP tools. There is deliberately
// no approve tool: only the user's click in the viewer approves a print (PRD FR-11).
package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/margarit/printpipe/internal/estimates"
	"github.com/margarit/printpipe/internal/jobs"
	"github.com/margarit/printpipe/internal/pipeline"
	"github.com/margarit/printpipe/internal/printer"
	"github.com/margarit/printpipe/internal/project"
	"github.com/margarit/printpipe/internal/scad"
	"github.com/margarit/printpipe/internal/sessions"
	"github.com/margarit/printpipe/internal/slicer"
)

const instructions = `This server turns OpenSCAD code into a print on the user's 3D printer.
Workflow: compile_model (look at the returned preview images and fix wrong geometry) →
optionally estimate_print → request_approval → wait for the user to click Approve in the
viewer at http://localhost:8080 → print_job. You cannot approve a print yourself.
Write models with named top-level parameters in OpenSCAD Customizer style, for example
"wall = 2; // [1:0.5:5] Wall thickness in mm", and change dimensions by passing params to
compile_model with empty scad_code instead of rewriting the file.`

type CompileIn struct {
	Project  string         `json:"project" jsonschema:"folder name: letters, digits, - and _"`
	Code     string         `json:"scad_code,omitempty" jsonschema:"complete OpenSCAD source with named top-level parameters; leave empty to recompile the latest version with new params"`
	Params   map[string]any `json:"params,omitempty" jsonschema:"optional parameter overrides, by top-level parameter name"`
	Estimate bool           `json:"estimate,omitempty" jsonschema:"also slice it for the default printer and report time, filament and cost"`
}

type ModelIn struct {
	Project string `json:"project" jsonschema:"project name"`
	Version int    `json:"version,omitempty" jsonschema:"version number; latest if empty"`
}

type ModelOut struct {
	Project string       `json:"project"`
	Version int          `json:"version"`
	Code    string       `json:"scad_code"`
	Params  []scad.Param `json:"params"`
}

type SliceOptions struct {
	Supports    *bool   `json:"supports,omitempty" jsonschema:"generate supports; the settings default if empty"`
	SupportType string  `json:"support_type,omitempty" jsonschema:"tree(auto) or normal(auto); the settings default if empty"`
	LayerHeight float64 `json:"layer_height,omitempty" jsonschema:"layer height in mm, e.g. 0.12, 0.2, 0.28; the profile's if empty"`
	Infill      *int    `json:"infill,omitempty" jsonschema:"sparse infill percent, 0 to 100; the profile's if empty"`
}

func (o SliceOptions) overrides() slicer.Overrides {
	return slicer.Overrides{Supports: o.Supports, SupportType: o.SupportType, LayerHeight: o.LayerHeight, Infill: o.Infill}
}

type EstimateIn struct {
	Project   string `json:"project"`
	Versions  []int  `json:"versions,omitempty" jsonschema:"versions to estimate; latest if empty"`
	PrinterID string `json:"printer_id,omitempty" jsonschema:"default printer if empty"`
	SliceOptions
}

// Tool output must be a JSON object, so the list is wrapped.
type EstimateOut struct {
	Estimates []estimates.Estimate `json:"estimates"`
	Summary   string               `json:"summary"`
}

type SliceIn struct {
	Project   string `json:"project"`
	Version   int    `json:"version,omitempty" jsonschema:"version to slice; latest if empty"`
	PrinterID string `json:"printer_id,omitempty" jsonschema:"default printer if empty"`
	SliceOptions
}

type ApprovalIn struct {
	Project   string `json:"project"`
	Version   int    `json:"version,omitempty" jsonschema:"version to print; latest if empty"`
	PrinterID string `json:"printer_id,omitempty" jsonschema:"default printer if empty"`
	SliceOptions
}

type ApprovalOut struct {
	JobID    string             `json:"job_id"`
	Printer  string             `json:"printer"`
	Estimate estimates.Estimate `json:"estimate"`
	Message  string             `json:"message"`
}

type PrintIn struct {
	JobID string `json:"job_id" jsonschema:"the job ID from request_approval"`
}

type PrintOut struct {
	JobID   string         `json:"job_id"`
	Printer string         `json:"printer"`
	Status  printer.Status `json:"status"`
	Message string         `json:"message"`
}

type PrinterIn struct {
	PrinterID string `json:"printer_id,omitempty" jsonschema:"default printer if empty"`
}

type PrintersOut struct {
	Printers []pipeline.PrinterInfo `json:"printers"`
}

type StatusOut struct {
	PrinterID string         `json:"printer_id,omitempty"`
	Status    printer.Status `json:"status"`
}

type Empty struct{}

type ProjectsOut struct {
	Projects []project.Summary `json:"projects"`
}

func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true}
}

// New returns the MCP server. One server serves every session (HTTP, bridge and stdio);
// reg, if set, is told about each session and its tool calls.
func New(svc *pipeline.Service, version string, reg *sessions.Registry) *mcp.Server {
	t := &tracker{reg: reg}
	s := mcp.NewServer(&mcp.Implementation{Name: "print-pipeline", Version: version}, &mcp.ServerOptions{
		Instructions:       instructions,
		InitializedHandler: t.initialized,
	})
	s.AddReceivingMiddleware(t.middleware)
	notDestructive := false

	mcp.AddTool(s, &mcp.Tool{
		Name:        "compile_model",
		Description: "Write model.scad as a new version of the project, compile it to STL with OpenSCAD and return preview images. Pass empty scad_code with params to change parameters of the latest version. The user watches at http://localhost:8080.",
		Annotations: &mcp.ToolAnnotations{Title: "Compile model", DestructiveHint: &notDestructive},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CompileIn) (*mcp.CallToolResult, any, error) {
		res, err := svc.Compile(ctx, in.Project, in.Code, in.Params)
		if err != nil {
			return nil, nil, err
		}
		if in.Estimate {
			if ests, err := svc.Estimate(ctx, in.Project, []int{res.Version}, "", slicer.Overrides{}); err == nil && len(ests) == 1 {
				res.Estimate = &ests[0]
			} else if err != nil {
				res.Warnings = append(res.Warnings, "estimate failed: "+err.Error())
			}
		}
		content := []mcp.Content{&mcp.TextContent{Text: res.Summary()}}
		for _, im := range res.Images {
			content = append(content, &mcp.ImageContent{Data: im.PNG, MIMEType: "image/png"})
		}
		return &mcp.CallToolResult{Content: content}, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_model",
		Description: "Return a version's OpenSCAD code and its top-level parameters with their values and ranges.",
		Annotations: readOnly("Get model"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ModelIn) (*mcp.CallToolResult, ModelOut, error) {
		v, err := svc.Version(in.Project, in.Version)
		if err != nil {
			return nil, ModelOut{}, err
		}
		code, err := readFile(v.Path(project.SCAD))
		if err != nil {
			return nil, ModelOut{}, err
		}
		params, err := svc.Params(in.Project, v.Number)
		if err != nil {
			return nil, ModelOut{}, err
		}
		return nil, ModelOut{Project: in.Project, Version: v.Number, Code: code, Params: params}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_projects",
		Description: "List projects in the workspace with their latest version.",
		Annotations: readOnly("List projects"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, ProjectsOut, error) {
		ps, err := svc.Projects()
		if ps == nil {
			ps = []project.Summary{}
		}
		return nil, ProjectsOut{Projects: ps}, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "estimate_print",
		Description: "Slice versions of a project and return print time, filament grams and metres, layers and cost. Use it to compare variants. Results are cached, so asking again is instant. Never creates or approves a print job.",
		Annotations: &mcp.ToolAnnotations{Title: "Estimate print", DestructiveHint: &notDestructive, IdempotentHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EstimateIn) (*mcp.CallToolResult, EstimateOut, error) {
		out, err := svc.Estimate(ctx, in.Project, in.Versions, in.PrinterID, in.overrides())
		if err != nil {
			return nil, EstimateOut{}, err
		}
		lines := make([]string, len(out))
		for i, e := range out {
			lines[i] = fmt.Sprintf("version %d on %s: %s", e.Version, e.PrinterID, e.Line())
		}
		return nil, EstimateOut{Estimates: out, Summary: strings.Join(lines, "\n")}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "slice_model",
		Description: "Slice one version with explicit options (supports on or off, layer height, infill) and report the estimate. The toolpaths appear in the viewer. Does not create or approve a print job.",
		Annotations: &mcp.ToolAnnotations{Title: "Slice model", DestructiveHint: &notDestructive, IdempotentHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SliceIn) (*mcp.CallToolResult, EstimateOut, error) {
		v := []int{}
		if in.Version > 0 {
			v = []int{in.Version}
		}
		out, err := svc.Estimate(ctx, in.Project, v, in.PrinterID, in.overrides())
		if err != nil {
			return nil, EstimateOut{}, err
		}
		return nil, EstimateOut{Estimates: out, Summary: out[0].Line()}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "request_approval",
		Description: "Slice a version for a printer and put an Approve button in the viewer with the estimate. Nothing is sent to the printer. Tell the user to review and click Approve, then call print_job with the returned job_id.",
		Annotations: &mcp.ToolAnnotations{Title: "Request approval", DestructiveHint: &notDestructive},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ApprovalIn) (*mcp.CallToolResult, ApprovalOut, error) {
		a, err := svc.RequestApproval(ctx, in.Project, in.Version, in.PrinterID, in.overrides())
		if err != nil {
			return nil, ApprovalOut{}, err
		}
		msg := fmt.Sprintf("Job %s for %s version %d on %s is waiting for approval: %s. Ask the user to review it at %s and click Approve, then call print_job with job_id %q.",
			a.Job.ID, a.Job.Project, a.Job.Version, a.PrinterName, a.Estimate.Line(), a.ViewerURL, a.Job.ID)
		return nil, ApprovalOut{JobID: a.Job.ID, Printer: a.PrinterName, Estimate: a.Estimate, Message: msg}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "print_job",
		Description: "Send an approved job to its printer and start printing. Fails with \"not approved yet\" until the user clicks Approve in the viewer. A job prints once.",
		Annotations: &mcp.ToolAnnotations{Title: "Print job", DestructiveHint: &notDestructive},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in PrintIn) (*mcp.CallToolResult, PrintOut, error) {
		res, err := svc.Print(ctx, strings.TrimSpace(in.JobID))
		if err != nil {
			if strings.Contains(err.Error(), jobs.ErrNotApproved.Error()) {
				return nil, PrintOut{}, fmt.Errorf("Not approved yet: ask the user to click Approve in the viewer (http://localhost:8080), then call print_job again")
			}
			return nil, PrintOut{}, err
		}
		msg := fmt.Sprintf("Printing on %s: the job was uploaded as %s and started. Progress shows in the viewer; call printer_status to check it.", res.Printer, res.Remote)
		return nil, PrintOut{JobID: res.Job.ID, Printer: res.Printer, Status: res.Status, Message: msg}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_printers",
		Description: "List configured printers with their model, build volume, job format and which one is the default.",
		Annotations: readOnly("List printers"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, PrintersOut, error) {
		ps, err := svc.Printers(ctx, false)
		return nil, PrintersOut{Printers: ps}, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "printer_status",
		Description: "Read a printer's state (idle, printing, paused, finished, failed, offline) and progress.",
		Annotations: readOnly("Printer status"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in PrinterIn) (*mcp.CallToolResult, StatusOut, error) {
		st, err := svc.Status(ctx, in.PrinterID)
		return nil, StatusOut{PrinterID: in.PrinterID, Status: st}, err
	})

	destructive := true
	mcp.AddTool(s, &mcp.Tool{
		Name:        "cancel_print",
		Description: "Stop the current print on a printer. Only call this when the user asks to stop the print.",
		Annotations: &mcp.ToolAnnotations{Title: "Cancel print", DestructiveHint: &destructive},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in PrinterIn) (*mcp.CallToolResult, StatusOut, error) {
		if err := svc.Cancel(ctx, in.PrinterID); err != nil {
			return nil, StatusOut{}, err
		}
		st, _ := svc.Status(ctx, in.PrinterID)
		return nil, StatusOut{PrinterID: in.PrinterID, Status: st}, nil
	})

	return s
}
