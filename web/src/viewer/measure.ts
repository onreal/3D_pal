import * as THREE from "three";
import { CSS2DObject } from "three/addons/renderers/CSS2DRenderer.js";
import { distance, snap } from "./geometry";

type P = [number, number, number];

/** Click two points on the part to measure the distance; clicks snap to nearby vertices (PRD V-6). */
export class Measure extends THREE.Group {
  enabled = false;
  private points: P[] = [];
  private raycaster = new THREE.Raycaster();
  private label?: CSS2DObject;

  constructor(private color: number, private onResult: (mmDistance: number | null) => void) {
    super();
    this.name = "measure";
  }

  setEnabled(on: boolean) {
    this.enabled = on;
    if (!on) this.reset();
  }

  reset() {
    this.points = [];
    this.label?.element.remove();
    this.label = undefined;
    for (const c of [...this.children]) {
      this.remove(c);
      const m = c as THREE.Mesh;
      m.geometry?.dispose();
      (m.material as THREE.Material | undefined)?.dispose();
    }
    this.onResult(null);
  }

  /** Handles a click at normalised device coordinates; returns true when it hit the part. */
  click(ndc: THREE.Vector2, camera: THREE.Camera, mesh: THREE.Mesh, snapRadius: number): boolean {
    if (!this.enabled) return false;
    this.raycaster.setFromCamera(ndc, camera);
    const hit = this.raycaster.intersectObject(mesh, false)[0];
    if (!hit || !hit.face) return false;
    const pos = mesh.geometry.getAttribute("position");
    const world = (i: number): P => {
      const v = new THREE.Vector3().fromBufferAttribute(pos, i).applyMatrix4(mesh.matrixWorld);
      return [v.x, v.y, v.z];
    };
    const p = snap([world(hit.face.a), world(hit.face.b), world(hit.face.c)], [hit.point.x, hit.point.y, hit.point.z], snapRadius);
    if (this.points.length >= 2) this.reset();
    this.points.push(p);
    this.add(this.marker(p, snapRadius));
    if (this.points.length === 2) {
      const [a, b] = this.points as [P, P];
      const g = new THREE.BufferGeometry().setFromPoints([new THREE.Vector3(...a), new THREE.Vector3(...b)]);
      const line = new THREE.Line(g, new THREE.LineBasicMaterial({ color: this.color, depthTest: false }));
      line.renderOrder = 10;
      this.add(line);
      const d = distance(a, b);
      const el = document.createElement("div");
      el.className = "dim-label measure";
      el.textContent = `${d.toFixed(2)} mm`;
      this.label = new CSS2DObject(el);
      this.label.position.set((a[0] + b[0]) / 2, (a[1] + b[1]) / 2, (a[2] + b[2]) / 2);
      this.add(this.label);
      this.onResult(d);
    }
    return true;
  }

  private marker(p: P, size: number) {
    const m = new THREE.Mesh(
      new THREE.SphereGeometry(Math.max(size * 0.35, 0.3), 16, 12),
      new THREE.MeshBasicMaterial({ color: this.color, depthTest: false }),
    );
    m.renderOrder = 11;
    m.position.set(...p);
    return m;
  }
}
