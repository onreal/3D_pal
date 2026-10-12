// =====================================================================
//  Wall Hook: parametric screw-on hook (house-format template, 1 part)
//  OpenSCAD 2021.01+ (pipeline: nightly, Manifold). Units: mm.
// =====================================================================
//  PARTS (part = ...)        count  print orientation
//    hook                    1      on its side: the profile lies on the bed, so the
//                                   hook bends along the layers, not across them
//    coupon_screw            1      test the screw hole and countersink first (~2 g)
//  HARDWARE
//    2 x countersunk wood screws, 4 mm (screw_d) with 8 mm heads (head_d)
//  ASSEMBLY ORDER
//    1. Screw the back plate to the wall, countersinks facing out.
//  PRINT SETTINGS
//    PLA or PETG, 0.2 mm layers, 4 walls, 20 % infill, no supports.
//  TUNING
//    screw too tight -> screw_clearance +0.1    head proud -> head_d +0.4
//    hook flexes     -> width +5 or arm_t +1    corners lift -> brim
// =====================================================================

/* [Part] */
part = "hook"; // [hook, coupon_screw]

/* [Size] */
width = 20;       // [10:1:60] Hook width (print height) in mm
plate_h = 60;     // [30:1:150] Back plate height in mm
plate_t = 5;      // [3:0.4:10] Back plate thickness in mm
reach = 35;       // [15:1:100] How far the hook sticks out from the wall in mm
arm_t = 6;        // [3:0.5:12] Arm thickness in mm
lip_h = 15;       // [5:1:40] Height of the upturned tip in mm
lip_t = 5;        // [3:0.5:10] Tip thickness in mm
fillet = 4;       // [0:0.5:10] Inside corner fillet in mm

/* [Screws] */
screws = 2;       // [1:1:3] Number of screws
screw_d = 4;      // [2:0.5:6] Screw shank diameter in mm
head_d = 8;       // [4:0.5:12] Countersunk head diameter in mm

/* [Fit] */
screw_clearance = 0.2; // [0:0.05:0.5] Gap per side around the screw shank in mm
elephant_foot = 0.2;   // [0:0.05:0.4] First-layer inset in mm

/* [Hidden] */
$fa = 2; $fs = 0.3;
eps = 0.01;
hole_r = screw_d / 2 + screw_clearance;
head_r = head_d / 2 + screw_clearance;
sink_depth = head_r - hole_r;                       // 90 degree countersink
screw_y = [for (i = [0:screws - 1]) arm_t + (plate_h - arm_t) * (i + 1) / (screws + 1)];

assert(plate_t >= sink_depth + 1.2, str("plate_t must be at least ", sink_depth + 1.2, " mm to fit the countersink"));
assert(reach > plate_t + lip_t + 5, "reach is too short to leave a gap inside the hook");
assert((plate_h - arm_t) / (screws + 1) >= head_r + 1, "screws are too close together: fewer screws or a taller plate");
assert(width <= 250 && plate_h <= 250, "too big for the bed");
echo(str("INFO: hook ", reach, " x ", plate_h, " x ", width, " mm, opening ", reach - plate_t - lip_t, " mm"));

// ------------------------------------------------------------- features
// Horizontal hole with a 45-degree pointed top, so its roof prints without supports.
// Drawn along +X; the point faces +Z.
module teardrop_2d(r) {
    union() {
        circle(r = r);
        rotate(45) square(r);
    }
}

// Screw hole along X through the back plate, countersunk on the hook side (x = plate_t).
module screw_hole() {
    rotate([0, 90, 0]) rotate(90) {
        translate([0, 0, -eps]) linear_extrude(plate_t + 2 * eps) teardrop_2d(hole_r);
    }
    translate([plate_t + eps, 0, 0]) rotate([0, -90, 0]) rotate(-90)
        linear_extrude(sink_depth + eps, scale = hole_r / head_r) teardrop_2d(head_r);
}

// Side profile: back plate, arm and upturned lip, with filleted inside corners
module profile_2d() {
    offset(r = -fillet / 4) offset(r = fillet / 4)       // round the outside corners a little
    offset(r = -fillet) offset(r = fillet)                // fillet the inside corners
    union() {
        square([plate_t, plate_h]);
        square([reach, arm_t]);
        translate([reach - lip_t, 0]) square([lip_t, lip_h]);
    }
}

// ---------------------------------------------------------------- parts
// In print orientation: the profile on the bed, extruded upwards by the hook width.
module hook() {
    difference() {
        union() {
            linear_extrude(0.2) offset(delta = -elephant_foot) profile_2d();
            translate([0, 0, 0.2 - eps]) linear_extrude(width - 0.2 + eps) profile_2d();
        }
        for (y = screw_y) translate([0, y, width / 2]) screw_hole();
    }
}

// The plate around one screw hole, to test the screw before printing the hook
module coupon_screw() {
    s = head_d + 6;
    translate([0, -screw_y[0] + s / 2, 0]) intersection() {
        hook();
        translate([-eps, screw_y[0] - s / 2, 0]) cube([plate_t + 2 * eps, s, width]);
    }
}

// ----------------------------------------------------------- dispatcher
if (part == "hook") hook();
else if (part == "coupon_screw") coupon_screw();
