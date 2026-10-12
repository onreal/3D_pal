import * as THREE from "three";
import type { Capabilities } from "../api";

// Z is up, as in OpenSCAD and the slicer.
THREE.Object3D.DEFAULT_UP.set(0, 0, 1);

export interface Palette {
  background: number;
  bed: number;
  grid: number;
  gridMajor: number;
  volume: number;
  part: [number, number, number];
  overhang: [number, number, number];
  warn: number;
  measure: number;
}

export function palette(dark: boolean): Palette {
  return dark
    ? { background: 0x15171a, bed: 0x23262b, grid: 0x30343a, gridMajor: 0x454a52, volume: 0x5a6270, part: [0.95, 0.46, 0.31], overhang: [0.93, 0.27, 0.6], warn: 0xff5d5d, measure: 0x6cc4ff }
    : { background: 0xf4f5f7, bed: 0xe3e6ea, grid: 0xd0d4da, gridMajor: 0xb3b9c1, volume: 0x8a93a0, part: [0.95, 0.46, 0.31], overhang: [0.84, 0.15, 0.52], warn: 0xd92d2d, measure: 0x1a73e8 };
}

/** The printer's bed and build volume at true scale, a 10 mm grid and axes (PRD V-1). */
export class Bed extends THREE.Group {
  volume = new THREE.Vector3(256, 256, 256);
  round = false;
  private cage?: THREE.LineSegments;

  constructor(private colors: Palette) {
    super();
    this.name = "bed";
  }

  setPrinter(caps: Pick<Capabilities, "build_volume" | "bed_shape"> | null) {
    const v = caps?.build_volume ?? { X: 256, Y: 256, Z: 256 };
    this.volume.set(v.X, v.Y, v.Z);
    this.round = caps?.bed_shape === "round";
    this.rebuild();
  }

  setPalette(p: Palette) {
    this.colors = p;
    this.rebuild();
  }

  /** Marks the build volume red when the part doesn't fit. */
  setOverflow(over: boolean) {
    if (this.cage) (this.cage.material as THREE.LineBasicMaterial).color.setHex(over ? this.colors.warn : this.colors.volume);
  }

  private rebuild() {
    for (const child of [...this.children]) {
      this.remove(child);
      disposeTree(child);
    }
    const { x, y, z } = this.volume;
    const c = this.colors;

    const plate = this.round
      ? new THREE.Mesh(new THREE.CircleGeometry(x / 2, 96), new THREE.MeshBasicMaterial({ color: c.bed }))
      : new THREE.Mesh(new THREE.PlaneGeometry(x, y), new THREE.MeshBasicMaterial({ color: c.bed }));
    plate.position.z = -0.05;
    plate.name = "plate";
    this.add(plate);

    // Grid in 10 mm steps, darker every 50 mm.
    const minor: number[] = [];
    const major: number[] = [];
    const hx = x / 2, hy = y / 2;
    for (let gx = -Math.floor(hx / 10) * 10; gx <= hx; gx += 10) (gx % 50 === 0 ? major : minor).push(gx, -hy, 0, gx, hy, 0);
    for (let gy = -Math.floor(hy / 10) * 10; gy <= hy; gy += 10) (gy % 50 === 0 ? major : minor).push(-hx, gy, 0, hx, gy, 0);
    this.add(lines(minor, c.grid, "grid"));
    this.add(lines(major, c.gridMajor, "grid-major"));

    const box = new THREE.BoxGeometry(x, y, z);
    box.translate(0, 0, z / 2);
    this.cage = new THREE.LineSegments(new THREE.EdgesGeometry(box), new THREE.LineBasicMaterial({ color: c.volume, transparent: true, opacity: 0.6 }));
    this.cage.name = "build-volume";
    box.dispose();
    this.add(this.cage);

    const axes = new THREE.AxesHelper(20);
    axes.position.set(-hx, -hy, 0.1);
    axes.name = "axes";
    this.add(axes);
  }
}

function lines(points: number[], color: number, name: string) {
  const g = new THREE.BufferGeometry();
  g.setAttribute("position", new THREE.Float32BufferAttribute(points, 3));
  const l = new THREE.LineSegments(g, new THREE.LineBasicMaterial({ color }));
  l.name = name;
  return l;
}

export function disposeTree(o: THREE.Object3D) {
  o.traverse((n) => {
    const m = n as THREE.Mesh;
    m.geometry?.dispose();
    const mat = m.material as THREE.Material | THREE.Material[] | undefined;
    if (Array.isArray(mat)) mat.forEach((x) => x.dispose());
    else mat?.dispose();
  });
}

export function lights(): THREE.Group {
  const g = new THREE.Group();
  g.add(new THREE.HemisphereLight(0xffffff, 0x8a8f99, 1.6));
  const key = new THREE.DirectionalLight(0xffffff, 1.6);
  key.position.set(150, -200, 300);
  g.add(key);
  const fill = new THREE.DirectionalLight(0xffffff, 0.5);
  fill.position.set(-200, 150, 100);
  g.add(fill);
  return g;
}
