import * as THREE from "three";

export type DisplayMode = "solid" | "wireframe" | "xray" | "edges";

export const DISPLAY_MODES: { mode: DisplayMode; label: string }[] = [
  { mode: "solid", label: "Solid" },
  { mode: "wireframe", label: "Wireframe" },
  { mode: "xray", label: "X-ray" },
  { mode: "edges", label: "Edges" },
];

/** Applies a display mode to the part's material and edge overlay (PRD V-4). */
export function applyMode(mode: DisplayMode, material: THREE.MeshStandardMaterial, edges: THREE.LineSegments | null) {
  material.wireframe = mode === "wireframe";
  material.transparent = mode === "xray";
  material.opacity = mode === "xray" ? 0.35 : 1;
  material.depthWrite = mode !== "xray";
  material.needsUpdate = true;
  if (edges) edges.visible = mode === "edges" || mode === "xray";
}

/** Feature edges (creases sharper than 20°) for the edges overlay. */
export function edgeLines(geometry: THREE.BufferGeometry, color: number): THREE.LineSegments {
  const lines = new THREE.LineSegments(
    new THREE.EdgesGeometry(geometry, 20),
    new THREE.LineBasicMaterial({ color, transparent: true, opacity: 0.75 }),
  );
  lines.name = "edges";
  lines.visible = false;
  return lines;
}
