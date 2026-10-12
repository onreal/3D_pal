// =====================================================================
//  Snap Box: box with a snap-on lid and slide-in dividers
//  House-format template for multi-part models.
//  OpenSCAD 2021.01+ (pipeline: nightly, Manifold). Units: mm.
// =====================================================================
//  PARTS (part = ...)        count       print orientation
//    base                    1           open side up
//    lid                     1           top face down (label engraved, mirrored)
//    divider                 dividers    flat
//    plate                   all parts arranged on one bed
//    coupon_snap             1 + 1       a wall strip and a lid strip: test the snap
//  ASSEMBLY ORDER
//    1. Base.  2. Slide the dividers into their slots.
//    3. Press the lid on until the beads click into the groove.
//  PRINT SETTINGS
//    PLA or PETG, 0.2 mm layers, 3 walls, 15 % infill, no supports.
//  TUNING
//    lid too tight   -> lid_clearance +0.05 or snap_bead -0.1
//    lid falls off   -> snap_bead +0.1
//    dividers stick  -> divider_clearance +0.05
// =====================================================================

/* [Part] */
part = "assembled"; // [assembled, exploded, section, base, lid, divider, plate, coupon_snap, check_interference]

/* [Size] */
inner_w = 80;      // [20:1:220] Inside width (X) in mm
inner_d = 50;      // [20:1:220] Inside depth (Y) in mm
inner_h = 30;      // [10:1:150] Inside height in mm
wall = 2;          // [1.2:0.4:4] Wall thickness in mm
floor_t = 1.6;     // [0.8:0.2:4] Floor thickness in mm
corner_r = 5;      // [2:0.5:15] Outside corner radius in mm

/* [Lid] */
lid_t = 1.6;       // [1.2:0.2:4] Lid top thickness in mm
lip_h = 6;         // [3:0.5:12] How far the lid skirt reaches into the base in mm
skirt_t = 1.6;     // [1.2:0.4:3] Lid skirt thickness in mm

/* [Dividers] */
dividers = 2;      // [0:1:6] Number of dividers
divider_t = 1.6;   // [1.2:0.2:3] Divider thickness in mm

/* [Fit] */
lid_clearance = 0.2;      // [0.1:0.05:0.5] Gap per side between skirt and base wall in mm
snap_bead = 0.4;          // [0.2:0.05:0.8] How far the lid's bead reaches into the groove in mm
divider_clearance = 0.2;  // [0.1:0.05:0.4] Gap per side in the divider slots in mm
axial_clearance = 0.2;    // [0.2:0.05:0.6] Gap above the dividers in mm
elephant_foot = 0.2;      // [0:0.05:0.4] First-layer inset in mm

/* [Cosmetic] */
label = "BOX";     // Engraved on the lid; empty for none
label_size = 8;    // [4:0.5:20] Label cap height in mm

/* [Hidden] */
$fa = 2; $fs = 0.3;
eps = 0.01;
outer_w = inner_w + 2 * wall;
outer_d = inner_d + 2 * wall;
base_h = floor_t + inner_h;
inner_r = corner_r - wall;
slot_depth = min(0.8, wall - 1.2);             // leaves at least 1.2 mm of wall
divider_h = inner_h - lip_h - axial_clearance;
divider_len = inner_d + 2 * (slot_depth - divider_clearance);
divider_x = [for (i = [0:max(0, dividers - 1)]) -inner_w / 2 + inner_w * (i + 1) / (dividers + 1)];
snap_z = lip_h / 2;                            // bead and groove height, measured down from the rim
snap_margin = corner_r + 6;                    // beads stop short of the stiff corners
explode = 20;

// Assembly timeline: build order for the viewer's Assembly tab
assembly = [
    ["base", "Base"],
    ["dividers", "Dividers (slide in)"],
    ["lid", "Lid (press on)"],
];
assembly_only = "";
module step(id) { if (assembly_only == "" || assembly_only == id) children(); }

assert(wall >= 1.2, "wall must be at least 1.2 mm");
assert(inner_r >= 0.5, "corner_r must be at least wall + 0.5 mm");
assert(lip_h <= inner_h - 5, "lip_h must leave at least 5 mm of inside height");
assert(dividers == 0 || slot_depth >= 0.4, "wall too thin for divider slots: make it at least 1.6 mm");
assert(dividers == 0 || inner_w / (dividers + 1) >= divider_t + 3, "dividers are too close together");
assert(outer_w <= 250 && outer_d <= 250 && base_h <= 250, "too big for the bed");
assert(min(inner_w, inner_d) > 2 * snap_margin + 5, "box too small for the snap beads: lower corner_r");
echo(str("INFO: outside ", outer_w, " x ", outer_d, " x ", base_h + lid_t, " mm closed"));
echo(str("INFO: ", dividers, " dividers ", divider_len, " x ", divider_h, " mm"));

// ------------------------------------------------------------- features
module rounded_rect(w, d, r) offset(r = r) square([w - 2 * r, d - 2 * r], center = true);

// First layer pulled in, so the bed face doesn't flare
module ef_extrude(h) {
    linear_extrude(0.2) offset(delta = -elephant_foot) children();
    translate([0, 0, 0.2 - eps]) linear_extrude(h - 0.2 + eps) children();
}

// Snap profile shared by the lid's bead and the base's groove: u is outward from the
// face, v is up. Both faces are 45 degrees, so it prints either way up and comes apart.
module snap_profile_2d(depth, grow = 0) {
    h = 2 * depth + 0.4;
    offset(delta = grow) polygon([[-1, -h / 2], [0, -h / 2], [depth, -0.2], [depth, 0.2], [0, h / 2], [-1, h / 2]]);
}

// The profile run along the straight part of each side of a w x d rectangle, facing out.
module snap_strips(w, d, depth, grow = 0) {
    for (s = [[0, d / 2, w], [180, d / 2, w], [90, w / 2, d], [270, w / 2, d]])
        rotate(s[0]) translate([0, s[1], 0])
            multmatrix([[0, 0, 1, 0], [1, 0, 0, 0], [0, 1, 0, 0]])
                linear_extrude(s[2] - 2 * snap_margin, center = true) snap_profile_2d(depth, grow);
}

// ---------------------------------------------------------------- parts
// Base: open side up
module base() {
    difference() {
        ef_extrude(base_h) rounded_rect(outer_w, outer_d, corner_r);
        translate([0, 0, floor_t]) linear_extrude(inner_h + eps) rounded_rect(inner_w, inner_d, inner_r);
        // groove for the lid's beads
        translate([0, 0, base_h - snap_z]) snap_strips(inner_w, inner_d, snap_bead + lid_clearance, lid_clearance);
        // divider slots
        if (dividers > 0) for (x = divider_x) for (sy = [-1, 1])
            translate([x - divider_t / 2 - divider_clearance, sy > 0 ? inner_d / 2 - eps : -inner_d / 2 - slot_depth, floor_t])
                cube([divider_t + 2 * divider_clearance, slot_depth + eps, inner_h + eps]);
    }
}

// Lid: top face down, skirt up. The label is engraved in the bed face, mirrored so it
// reads correctly on the closed box.
module lid() {
    sw = inner_w - 2 * lid_clearance;
    sd = inner_d - 2 * lid_clearance;
    difference() {
        union() {
            ef_extrude(lid_t) rounded_rect(outer_w, outer_d, corner_r);
            translate([0, 0, lid_t - eps]) linear_extrude(lip_h + eps) difference() {
                rounded_rect(sw, sd, max(0.5, inner_r - lid_clearance));
                rounded_rect(sw - 2 * skirt_t, sd - 2 * skirt_t, max(0.5, inner_r - lid_clearance - skirt_t));
            }
        }
        if (label != "") translate([0, 0, -eps]) linear_extrude(0.6 + eps) mirror([1, 0, 0])
            text(label, size = label_size, font = "Liberation Sans:style=Bold", halign = "center", valign = "center");
    }
    translate([0, 0, lid_t + snap_z]) snap_strips(sw, sd, snap_bead);
}

// Divider: flat on the bed, length along X, height along Y
module divider() {
    translate([-divider_len / 2, -divider_h / 2, 0]) ef_extrude(divider_t) square([divider_len, divider_h]);
}

// ---------------------------------------------------------- placements
// Each part moved from its print orientation to where it sits in the box.
module base_placed() base();
// The lid is turned over (rotated, never mirrored: a mirror would make a mirror-image part).
module lid_placed(e = 0) translate([0, 0, base_h + lid_t + e]) rotate([0, 180, 0]) lid();
module divider_placed(i, e = 0)
    translate([divider_x[i], 0, floor_t + divider_h / 2 + e]) rotate([90, 0, 90]) translate([0, 0, -divider_t / 2]) divider();

module assembled(e = 0) {
    step("base") color("#ff7a1a") base_placed();
    step("dividers") color("#4a4a52") if (dividers > 0) for (i = [0:dividers - 1]) divider_placed(i, e);
    step("lid") color("#ffc078") lid_placed(2 * e);
}

module plate() {
    gap = 8;
    base();
    translate([0, outer_d + gap, 0]) lid();
    if (dividers > 0) for (i = [0:dividers - 1])
        translate([0, -outer_d / 2 - gap - divider_h / 2 - i * (divider_h + gap), 0]) divider();
}

// A strip of base wall and a strip of lid, to try the snap before printing the box
module coupon_snap() {
    len = 30;
    translate([0, 0, -(base_h - lip_h - 4)]) intersection() {
        base();
        translate([-len / 2, inner_d / 2 - eps, base_h - lip_h - 4]) cube([len, wall + 1, lip_h + 5]);
    }
    translate([0, -12, 0]) intersection() {
        lid();
        translate([-len / 2, inner_d / 2 - 6, -eps]) cube([len, wall + 7, lid_t + lip_h + 1]);
    }
}

// Every pair that could touch. Clean = only the 2 mm marker cube. Parts that rest on
// each other by design (lid on rim, dividers on the floor) are lifted by eps, because the
// Manifold engine reports touching faces as a paper-thin overlap.
module check_interference() {
    intersection() { base_placed(); lid_placed(eps); }
    if (dividers > 0) for (i = [0:dividers - 1]) {
        intersection() { base_placed(); divider_placed(i, eps); }
        intersection() { lid_placed(eps); divider_placed(i, eps); }
    }
    cube(2, center = true);
}

// ----------------------------------------------------------- dispatcher
if (part == "assembled") assembled();
else if (part == "exploded") assembled(explode);
else if (part == "section") difference() { assembled(); translate([-500, 0, -500]) cube(1000); }
else if (part == "base") base();
else if (part == "lid") lid();
else if (part == "divider") divider();
else if (part == "plate") plate();
else if (part == "coupon_snap") coupon_snap();
else if (part == "check_interference") check_interference();
