// Package scad compiles OpenSCAD models headless and renders preview images (PRD FR-3 to FR-5).
package scad

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/margarit/printpipe/internal/mesh"
	"github.com/margarit/printpipe/internal/runner"
)

type Compiler struct {
	run    runner.Runner
	binary string
}

func New(r runner.Runner, binary string) *Compiler {
	if binary == "" {
		binary = "openscad"
	}
	return &Compiler{run: r, binary: binary}
}

// Cameras for preview angles, as OpenSCAD gimbal rotations (x, y, z in degrees).
var Cameras = map[string][3]int{
	"iso":   {55, 0, 25},
	"front": {90, 0, 0},
	"right": {90, 0, 90},
	"top":   {0, 0, 0},
}

// Compile writes stl from scad with the given -D arguments. A non-zero exit or an empty
// mesh returns OpenSCAD's own message so the agent can fix the code.
func (c *Compiler) Compile(ctx context.Context, scadPath, stlPath string, defines []string) (*mesh.Mesh, error) {
	os.Remove(stlPath)
	args := append([]string{"-o", stlPath, "--export-format", "binstl"}, defines...)
	args = append(args, scadPath)
	out, err := c.run.Run(ctx, c.binary, args...)
	if err != nil {
		return nil, fmt.Errorf("OpenSCAD could not compile the model:\n%s", cleanError(err.Error()))
	}
	m, err := mesh.ReadFile(stlPath)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, mesh.ErrEmpty) {
		return nil, fmt.Errorf("OpenSCAD produced no geometry: the top-level object is empty. Make sure the file calls a module or draws a shape at the top level.%s", tail(out))
	}
	if err != nil {
		return nil, fmt.Errorf("reading the compiled STL: %w", err)
	}
	return m, nil
}

// Render draws a PNG of the compiled STL from one preview angle, so the agent sees
// exactly the geometry that will be sliced.
func (c *Compiler) Render(ctx context.Context, stlPath, pngPath, angle string) error {
	cam, ok := Cameras[angle]
	if !ok {
		return fmt.Errorf("unknown preview angle %q", angle)
	}
	wrapper := strings.TrimSuffix(pngPath, ".png") + ".view.scad"
	src := fmt.Sprintf("color(\"#F2754E\") import(%q, convexity = 10);\n", filepath.Base(stlPath))
	if err := os.WriteFile(wrapper, []byte(src), 0o644); err != nil {
		return err
	}
	defer os.Remove(wrapper)
	// The STL must sit beside the wrapper, since import() paths are relative to it.
	if filepath.Dir(stlPath) != filepath.Dir(wrapper) {
		return errors.New("render: the PNG must be written next to the STL")
	}
	_, err := c.run.Run(ctx, c.binary,
		"-o", pngPath,
		"--imgsize=800,600",
		"--autocenter", "--viewall",
		fmt.Sprintf("--camera=0,0,0,%d,%d,%d,0", cam[0], cam[1], cam[2]),
		"--colorscheme=Tomorrow",
		"--projection=p",
		wrapper)
	if err != nil {
		return fmt.Errorf("rendering the %s preview failed (is a display available? the image starts Xvfb):\n%s", angle, cleanError(err.Error()))
	}
	return nil
}

var noise = regexp.MustCompile(`(?m)^(Geometries in cache|Geometry cache size|CGAL Polyhedrons in cache|CGAL cache size|Total rendering time|Compiling design|Parsing design|Rendering Polygon Mesh|Top level object is a|Simple:|Vertices:|Halfedges:|Edges:|Halffacets:|Facets:|Volumes:|Normalized tree|Rendering finished|Used file cache size).*\n?`)

// cleanError keeps OpenSCAD's errors and warnings and drops progress lines.
func cleanError(msg string) string {
	msg = noise.ReplaceAllString(msg, "")
	return strings.TrimSpace(msg)
}

func tail(out []byte) string {
	s := strings.TrimSpace(cleanError(string(out)))
	if s == "" {
		return ""
	}
	return "\n" + s
}
