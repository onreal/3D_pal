import * as THREE from "three";
import { STLLoader } from "three/addons/loaders/STLLoader.js";
import { CSS2DRenderer } from "three/addons/renderers/CSS2DRenderer.js";
import type { Capabilities, Vec } from "../api";
import { Controls, type View } from "./controls";
import { Dimensions } from "./dimensions";
import { fitsVolume, overflowAxes, placeOnBedOffset } from "./geometry";
import { Measure } from "./measure";
import { applyMode, edgeLines, type DisplayMode } from "./modes";
import { overhangColors, overhangFaces, overhangShare } from "./overhangs";
import { Bed, disposeTree, lights, palette, type Palette } from "./scene";
import { Section, type Axis } from "./section";

export interface ModelInfo {
  size: Vec;
  fits: boolean;
  overflow: string[];
  overhangShare: number;
  triangles: number;
}

/** The 3D workspace: the part on a true-scale bed, with inspection tools (PRD V-1 to V-8). */
export class Viewer {
  readonly renderer: THREE.WebGLRenderer;
  readonly camera = new THREE.PerspectiveCamera(40, 1, 0.1, 10000);
  readonly scene = new THREE.Scene();
  readonly controls: Controls;
  private labels = new CSS2DRenderer();
  private colors: Palette;
  private bed: Bed;
  private dims: Dimensions;
  private measureTool: Measure;
  private section = new Section();
  private mesh: THREE.Mesh | null = null;
  private edges: THREE.LineSegments | null = null;
  private material: THREE.MeshStandardMaterial;
  private box = new THREE.Box3();
  private mode: DisplayMode = "solid";
  private overhangAngle = 45;
  private showOverhangs = true;
  private frame = 0;
  private resizeObserver: ResizeObserver;
  info: ModelInfo | null = null;
  onInfo: (info: ModelInfo) => void = () => {};
  onMeasure: (d: number | null) => void = () => {};

  constructor(private container: HTMLElement, dark = window.matchMedia?.("(prefers-color-scheme: dark)").matches ?? false) {
    this.colors = palette(dark);
    this.renderer = new THREE.WebGLRenderer({ antialias: true, preserveDrawingBuffer: true });
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
    this.renderer.localClippingEnabled = true;
    this.renderer.domElement.className = "viewer-canvas";
    container.appendChild(this.renderer.domElement);
    this.labels.domElement.className = "viewer-labels";
    container.appendChild(this.labels.domElement);

    this.scene.background = new THREE.Color(this.colors.background);
    this.scene.add(lights());
    this.bed = new Bed(this.colors);
    this.bed.setPrinter(null);
    this.scene.add(this.bed);
    this.dims = new Dimensions(this.colors.volume, this.colors.warn);
    this.scene.add(this.dims);
    this.measureTool = new Measure(this.colors.measure, (d) => {
      this.onMeasure(d);
      this.render();
    });
    this.scene.add(this.measureTool);
    this.material = new THREE.MeshStandardMaterial({ vertexColors: true, metalness: 0.05, roughness: 0.6, side: THREE.DoubleSide });

    this.controls = new Controls(this.camera, this.renderer.domElement, () => this.render());
    this.controls.frame(new THREE.Box3(new THREE.Vector3(-128, -128, 0), new THREE.Vector3(128, 128, 60)));
    this.resizeObserver = new ResizeObserver(() => this.resize());
    this.resizeObserver.observe(container);
    this.resize();
    this.controls.fit("iso");
    this.renderer.domElement.addEventListener("click", (e) => this.handleClick(e));
  }

  get hasModel() {
    return this.mesh !== null;
  }

  setPrinter(caps: Pick<Capabilities, "build_volume" | "bed_shape"> | null) {
    this.bed.setPrinter(caps);
    if (this.mesh) this.updateInfo();
    this.render();
  }

  /** Loads an STL. Only the first model frames the camera; later ones keep the user's view. */
  async showModel(url: string): Promise<ModelInfo> {
    const res = await fetch(url);
    if (!res.ok) throw new Error(`could not load the model (${res.status})`);
    const geo = new STLLoader().parse(await res.arrayBuffer());
    return this.setGeometry(geo);
  }

  setGeometry(geo: THREE.BufferGeometry): ModelInfo {
    if (geo.index) geo = geo.toNonIndexed();
    geo.computeBoundingBox();
    const [dx, dy, dz] = placeOnBedOffset({
      min: geo.boundingBox!.min.toArray() as [number, number, number],
      max: geo.boundingBox!.max.toArray() as [number, number, number],
    });
    geo.translate(dx, dy, dz);
    // Flat face normals from the geometry itself: STL files often carry zero or wrong normals,
    // and the overhang check depends on them.
    geo.deleteAttribute("normal");
    geo.computeVertexNormals();
    geo.computeBoundingBox();
    geo.computeBoundingSphere();

    const first = !this.mesh;
    this.replaceMesh(geo);
    this.box.copy(geo.boundingBox!);
    this.controls.frame(this.box);
    this.measureTool.reset();
    this.applyOverhangs();
    this.applySection();
    this.updateInfo();
    if (first) this.controls.fit("iso");
    this.render();
    return this.info!;
  }

  private replaceMesh(geo: THREE.BufferGeometry) {
    if (this.mesh) {
      this.scene.remove(this.mesh);
      this.mesh.geometry.dispose();
    }
    if (this.edges) {
      this.scene.remove(this.edges);
      disposeTree(this.edges);
    }
    this.mesh = new THREE.Mesh(geo, this.material);
    this.mesh.name = "part";
    this.scene.add(this.mesh);
    this.edges = edgeLines(geo, 0x1b1d21);
    this.scene.add(this.edges);
    applyMode(this.mode, this.material, this.edges);
  }

  private applyOverhangs() {
    if (!this.mesh) return;
    const geo = this.mesh.geometry;
    const pos = geo.getAttribute("position").array;
    const nrm = geo.getAttribute("normal").array;
    const flags = this.showOverhangs ? overhangFaces(pos, nrm, this.overhangAngle) : new Array<boolean>(pos.length / 9).fill(false);
    geo.setAttribute("color", new THREE.BufferAttribute(overhangColors(flags, this.colors.part, this.colors.overhang), 3));
    geo.userData.overhangShare = overhangShare(flags);
  }

  private updateInfo() {
    if (!this.mesh) return;
    const s = this.box.getSize(new THREE.Vector3());
    const size = { X: s.x, Y: s.y, Z: s.z };
    const vol = { X: this.bed.volume.x, Y: this.bed.volume.y, Z: this.bed.volume.z };
    const fits = fitsVolume(size, vol, this.bed.round ? "round" : "rect");
    this.bed.setOverflow(!fits);
    this.dims.update(this.box, !fits);
    this.info = {
      size,
      fits,
      overflow: fits ? [] : overflowAxes(size, vol),
      overhangShare: (this.mesh.geometry.userData.overhangShare as number) ?? 0,
      triangles: this.mesh.geometry.getAttribute("position").count / 3,
    };
    this.onInfo(this.info);
  }

  setMode(mode: DisplayMode) {
    this.mode = mode;
    applyMode(mode, this.material, this.edges);
    this.render();
  }

  setOverhangs(on: boolean, angle = this.overhangAngle) {
    this.showOverhangs = on;
    this.overhangAngle = angle;
    this.applyOverhangs();
    this.updateInfo();
    this.render();
  }

  setSection(axis: Axis | null, t = this.section.t) {
    this.section.axis = axis;
    this.section.t = t;
    this.applySection();
    this.render();
  }

  private applySection() {
    this.material.clippingPlanes = this.section.update(this.section.axis, this.section.t, this.box);
    this.material.needsUpdate = true;
  }

  setMeasuring(on: boolean) {
    this.measureTool.setEnabled(on);
    this.renderer.domElement.style.cursor = on ? "crosshair" : "";
    this.render();
  }

  setView(view: View) {
    this.controls.setView(view);
    this.render();
  }

  fit() {
    this.controls.fit();
    this.render();
  }

  private handleClick(e: MouseEvent) {
    if (!this.mesh || !this.measureTool.enabled) return;
    const r = this.renderer.domElement.getBoundingClientRect();
    const ndc = new THREE.Vector2(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
    const radius = Math.max(this.box.getSize(new THREE.Vector3()).length() * 0.02, 0.5);
    this.measureTool.click(ndc, this.camera, this.mesh, radius);
    this.render();
  }

  /** The current view as a PNG (PRD V-13). */
  screenshot(): Promise<Blob> {
    this.renderNow();
    return new Promise((resolve, reject) =>
      this.renderer.domElement.toBlob((b) => (b ? resolve(b) : reject(new Error("screenshot failed"))), "image/png"),
    );
  }

  setDark(dark: boolean) {
    this.colors = palette(dark);
    this.scene.background = new THREE.Color(this.colors.background);
    this.bed.setPalette(this.colors);
    this.applyOverhangs();
    this.updateInfo();
    this.render();
  }

  private resize() {
    const w = Math.max(this.container.clientWidth, 1);
    const h = Math.max(this.container.clientHeight, 1);
    this.renderer.setSize(w, h, false);
    this.labels.setSize(w, h);
    this.camera.aspect = w / h;
    this.camera.updateProjectionMatrix();
    this.renderNow();
  }

  render() {
    if (this.frame) return;
    this.frame = requestAnimationFrame(() => {
      this.frame = 0;
      this.renderNow();
    });
  }

  private renderNow() {
    this.renderer.render(this.scene, this.camera);
    this.labels.render(this.scene, this.camera);
  }

  dispose() {
    this.resizeObserver.disconnect();
    this.controls.dispose();
    disposeTree(this.scene);
    this.renderer.dispose();
  }
}
