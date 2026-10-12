import * as THREE from "three";
import { STLLoader } from "three/addons/loaders/STLLoader.js";
import type { AssemblyPart, AssemblyResult, Capabilities } from "../api";
import { Controls, type View } from "../viewer/controls";
import { Bed, disposeTree, lights, palette } from "../viewer/scene";
import { approachDirection, offsetAt, partState } from "./timeline";

// Distinct colours for parts the model left uncoloured.
const FALLBACK = ["#F2754E", "#3C8DE8", "#2BB07F", "#B565D9", "#E8B13C", "#E85C8A", "#5CC7D6", "#8A8F99"];

type V3 = [number, number, number];

interface Placed {
  part: AssemblyPart;
  group: THREE.Group; // moved along the approach path
  solid: THREE.Mesh;
  ghost: THREE.Mesh;
  direction: V3;
}

/** The parts of an assembly on the printer's bed, moved by the timeline (t from 0 to n). */
export class AssemblyView {
  readonly renderer: THREE.WebGLRenderer;
  readonly camera = new THREE.PerspectiveCamera(40, 1, 0.1, 10000);
  readonly scene = new THREE.Scene();
  private controls: Controls;
  private bed: Bed;
  private root = new THREE.Group();
  private placed: Placed[] = [];
  private distance = 60;
  private frame = 0;
  private t = 0;
  ghosts = true;
  private resizeObserver: ResizeObserver;

  constructor(private container: HTMLElement, dark = window.matchMedia?.("(prefers-color-scheme: dark)").matches ?? false) {
    const colors = palette(dark);
    this.renderer = new THREE.WebGLRenderer({ antialias: true, preserveDrawingBuffer: true });
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
    this.renderer.domElement.className = "viewer-canvas";
    container.appendChild(this.renderer.domElement);
    this.scene.background = new THREE.Color(colors.background);
    this.scene.add(lights());
    this.bed = new Bed(colors);
    this.bed.setPrinter(null);
    this.scene.add(this.bed, this.root);
    this.controls = new Controls(this.camera, this.renderer.domElement, () => this.render());
    this.resizeObserver = new ResizeObserver(() => this.resize());
    this.resizeObserver.observe(container);
    this.resize();
    this.controls.fit("iso");
  }

  setPrinter(caps: Pick<Capabilities, "build_volume" | "bed_shape"> | null) {
    this.bed.setPrinter(caps);
    this.render();
  }

  get count() {
    return this.placed.length;
  }

  /** Loads every part's STL; the order of `parts` is the build order. */
  async load(result: AssemblyResult, parts: AssemblyPart[], url: (p: AssemblyPart) => string) {
    this.clear();
    const loader = new STLLoader();
    const min = result.min, max = result.max;
    // Centre the whole assembly on the bed and put its lowest point at Z = 0, keeping every
    // part's position relative to the others.
    const shift = new THREE.Vector3(-(min.X + max.X) / 2, -(min.Y + max.Y) / 2, -min.Z);
    const size = Math.max(max.X - min.X, max.Y - min.Y, max.Z - min.Z, 10);
    this.distance = size * 0.9;
    const center: V3 = [0, 0, (max.Z - min.Z) / 2];

    const loaded = await Promise.all(parts.map(async (p) => {
      if (p.error) return null;
      const res = await fetch(url(p));
      if (!res.ok) return null;
      const geo = loader.parse(await res.arrayBuffer());
      geo.translate(shift.x, shift.y, shift.z);
      geo.deleteAttribute("normal");
      geo.computeVertexNormals();
      geo.computeBoundingBox();
      return { p, geo };
    }));
    loaded.forEach((item, order) => {
      if (!item) return;
      const color = new THREE.Color(item.p.color || FALLBACK[order % FALLBACK.length]!);
      const solid = new THREE.Mesh(item.geo, new THREE.MeshStandardMaterial({ color, metalness: 0.05, roughness: 0.55, side: THREE.DoubleSide }));
      const ghost = new THREE.Mesh(item.geo, new THREE.MeshBasicMaterial({ color, transparent: true, opacity: 0.12, depthWrite: false }));
      const group = new THREE.Group();
      group.add(solid);
      this.root.add(ghost, group);
      const c = item.geo.boundingBox!.getCenter(new THREE.Vector3());
      this.placed.push({ part: item.p, group, solid, ghost, direction: approachDirection([c.x, c.y, c.z], center) });
    });
    this.controls.frame(new THREE.Box3(new THREE.Vector3(-size / 2, -size / 2, 0), new THREE.Vector3(size / 2, size / 2, (max.Z - min.Z) + size * 0.5)));
    this.controls.fit("iso");
    this.setTime(this.t);
  }

  /** Moves every part to where it is at time t (0 = nothing placed, n = all placed). */
  setTime(t: number) {
    this.t = t;
    this.placed.forEach((p, i) => {
      const s = partState(i, t);
      const [x, y, z] = offsetAt(i, t, p.direction, this.distance);
      p.group.position.set(x, y, z);
      p.group.visible = s.kind !== "hidden";
      p.ghost.visible = this.ghosts && s.kind !== "placed";
    });
    this.render();
  }

  /** See-through parts, to watch inner parts go in behind a housing. */
  setXray(on: boolean) {
    for (const p of this.placed) {
      const m = p.solid.material as THREE.MeshStandardMaterial;
      m.transparent = on;
      m.opacity = on ? 0.4 : 1;
      m.depthWrite = !on;
      m.needsUpdate = true;
    }
    this.render();
  }

  setGhosts(on: boolean) {
    this.ghosts = on;
    this.setTime(this.t);
  }

  /** Highlights one step's part (the current one, or a hovered list row). */
  highlight(index: number) {
    this.placed.forEach((p, i) => {
      const m = p.solid.material as THREE.MeshStandardMaterial;
      m.emissive.setHex(i === index ? 0x332211 : 0x000000);
    });
    this.render();
  }

  setView(v: View) {
    this.controls.setView(v);
    this.render();
  }

  screenshot(): Promise<Blob> {
    this.renderer.render(this.scene, this.camera);
    return new Promise((resolve, reject) => this.renderer.domElement.toBlob((b) => (b ? resolve(b) : reject(new Error("screenshot failed"))), "image/png"));
  }

  private clear() {
    for (const p of this.placed) {
      p.solid.geometry.dispose();
      (p.solid.material as THREE.Material).dispose();
      (p.ghost.material as THREE.Material).dispose();
    }
    this.root.clear();
    this.placed = [];
  }

  private resize() {
    const w = Math.max(this.container.clientWidth, 1);
    const h = Math.max(this.container.clientHeight, 1);
    this.renderer.setSize(w, h, false);
    this.camera.aspect = w / h;
    this.camera.updateProjectionMatrix();
    this.renderer.render(this.scene, this.camera);
  }

  render() {
    if (this.frame) return;
    this.frame = requestAnimationFrame(() => {
      this.frame = 0;
      this.renderer.render(this.scene, this.camera);
    });
  }

  dispose() {
    this.clear();
    this.resizeObserver.disconnect();
    this.controls.dispose();
    disposeTree(this.scene);
    this.renderer.dispose();
  }
}
