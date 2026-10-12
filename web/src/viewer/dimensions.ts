import * as THREE from "three";
import { CSS2DObject } from "three/addons/renderers/CSS2DRenderer.js";
import type { Vec } from "../api";
import { mm } from "../format";

function label(text: string, cls: string): CSS2DObject {
  const el = document.createElement("div");
  el.className = `dim-label ${cls}`;
  el.textContent = text;
  return new CSS2DObject(el);
}

/** Bounding box with width, depth and height labels in mm (PRD V-5). */
export class Dimensions extends THREE.Group {
  private box?: THREE.LineSegments;
  private labels: CSS2DObject[] = [];

  constructor(private color: number, private warn: number) {
    super();
    this.name = "dimensions";
  }

  update(b: THREE.Box3, over: boolean) {
    this.clear();
    const size = b.getSize(new THREE.Vector3());
    const center = b.getCenter(new THREE.Vector3());
    const geo = new THREE.BoxGeometry(Math.max(size.x, 0.01), Math.max(size.y, 0.01), Math.max(size.z, 0.01));
    geo.translate(center.x, center.y, center.z);
    this.box = new THREE.LineSegments(new THREE.EdgesGeometry(geo), new THREE.LineDashedMaterial({ color: over ? this.warn : this.color, dashSize: 2, gapSize: 1.5, transparent: true, opacity: 0.8 }));
    this.box.computeLineDistances();
    geo.dispose();
    this.add(this.box);

    const cls = over ? "over" : "";
    const w = label(`${mm(size.x)} mm`, cls);
    w.position.set(center.x, b.min.y, b.min.z);
    const d = label(`${mm(size.y)} mm`, cls);
    d.position.set(b.max.x, center.y, b.min.z);
    const h = label(`${mm(size.z)} mm`, cls);
    h.position.set(b.max.x, b.min.y, center.z);
    this.labels = [w, d, h];
    this.add(w, d, h);
  }

  override clear(): this {
    for (const l of this.labels) l.element.remove();
    this.labels = [];
    if (this.box) {
      this.box.geometry.dispose();
      (this.box.material as THREE.Material).dispose();
    }
    return super.clear();
  }

  static sizeOf(b: THREE.Box3): Vec {
    const s = b.getSize(new THREE.Vector3());
    return { X: s.x, Y: s.y, Z: s.z };
  }
}
