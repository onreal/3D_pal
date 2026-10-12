import * as THREE from "three";
import { OrbitControls } from "three/addons/controls/OrbitControls.js";

export type View = "iso" | "top" | "front" | "right";

/** Direction from the target to the camera for each preset view (Z up). */
export const VIEW_DIRECTIONS: Record<View, [number, number, number]> = {
  iso: [1, -1.2, 0.9],
  top: [0, -0.0001, 1],
  front: [0, -1, 0.0001],
  right: [1, 0, 0.0001],
};

/** Distance at which a sphere of `radius` fills the view of a camera with vertical `fovDeg`. */
export function fitDistance(radius: number, fovDeg: number, aspect: number): number {
  const v = (fovDeg * Math.PI) / 180;
  const h = 2 * Math.atan(Math.tan(v / 2) * aspect);
  const fov = Math.min(v, h);
  return (radius / Math.sin(fov / 2)) * 1.1;
}

/** Orbit, pan and zoom, preset views and fit-to-model (PRD V-3). */
export class Controls {
  readonly orbit: OrbitControls;
  private target = new THREE.Box3(new THREE.Vector3(-20, -20, 0), new THREE.Vector3(20, 20, 40));

  constructor(private camera: THREE.PerspectiveCamera, dom: HTMLElement, onChange: () => void) {
    this.orbit = new OrbitControls(camera, dom);
    this.orbit.enableDamping = false;
    this.orbit.screenSpacePanning = true;
    this.orbit.addEventListener("change", onChange);
  }

  /** Remembers what "fit" frames: the model, or the bed when there is none. */
  frame(box: THREE.Box3) {
    this.target.copy(box);
  }

  fit(view?: View) {
    const center = this.target.getCenter(new THREE.Vector3());
    const radius = Math.max(this.target.getSize(new THREE.Vector3()).length() / 2, 5);
    const dir = view ? new THREE.Vector3(...VIEW_DIRECTIONS[view]) : this.camera.position.clone().sub(this.orbit.target);
    if (dir.lengthSq() < 1e-9) dir.set(...VIEW_DIRECTIONS.iso);
    dir.normalize();
    const dist = fitDistance(radius, this.camera.fov, this.camera.aspect);
    this.camera.position.copy(center).addScaledVector(dir, dist);
    this.camera.near = Math.max(0.1, dist / 1000);
    this.camera.far = dist * 100;
    this.camera.updateProjectionMatrix();
    this.orbit.target.copy(center);
    this.orbit.update();
  }

  setView(view: View) {
    this.fit(view);
  }

  dispose() {
    this.orbit.dispose();
  }
}
