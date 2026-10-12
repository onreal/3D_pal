// Calibration cube with chamfered top edges.
size = 20;    // [5:1:100] Edge length in mm
chamfer = 2;  // [0:0.5:5] Top chamfer in mm

hull() {
  cube([size, size, size - chamfer]);
  translate([chamfer, chamfer, 0]) cube([size - 2 * chamfer, size - 2 * chamfer, size]);
}
