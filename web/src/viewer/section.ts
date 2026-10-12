import * as THREE from "three";

export type Axis = "x" | "y" | "z";

/** A movable clipping plane on X, Y or Z to look inside walls and holes (PRD V-7). */
export class Section {
  readonly plane = new THREE.Plane(new THREE.Vector3(0, 0, -1), 0);
  axis: Axis | null = null;
  /** Position along the axis, 0 (part minimum) to 1 (part maximum). */
  t = 0.5;

  /** Keeps everything below the plane on the chosen axis; returns the planes for the material. */
  update(axis: Axis | null, t: number, box: THREE.Box3): THREE.Plane[] {
    this.axis = axis;
    this.t = t;
    if (!axis) return [];
    const k = axis === "x" ? "x" : axis === "y" ? "y" : "z";
    const at = box.min[k] + (box.max[k] - box.min[k]) * t;
    const normal = new THREE.Vector3(k === "x" ? -1 : 0, k === "y" ? -1 : 0, k === "z" ? -1 : 0);
    this.plane.set(normal, at);
    return [this.plane];
  }
}
