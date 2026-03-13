// ============================================================
// suzuha physical agent — pan-tilt mechanism
// ============================================================
// Print all 3 parts separately, no supports needed if printed
// in the orientations shown.
//
// Assembly:
//   1. Press-fit pan servo into base
//   2. Screw tilt bracket onto pan servo horn
//   3. Press-fit tilt servo into tilt bracket
//   4. Screw head onto tilt servo horn
//   5. Mount camera on head with M2 screws
//   6. Mount ESP32-P4-NANO on base with M2.5 screws (or standoffs)
//
// Set `part` variable to render each part:
//   0 = assembly view (all parts)
//   1 = base
//   2 = tilt bracket
//   3 = head (camera mount)

part = 0;

// ============================================================
// Parameters — adjust to match your hardware
// ============================================================

// --- SG90 Servo ---
sg90_w     = 22.8;   // width (along shaft axis)
sg90_d     = 12.2;   // depth
sg90_h     = 22.5;   // height (body only)
sg90_flange_w = 32.2; // flange width (with mounting ears)
sg90_flange_h = 2.5;  // flange thickness
sg90_flange_z = 15.5; // flange bottom from servo bottom
sg90_shaft_offset = 6.0; // shaft center from edge

// --- ESP32-P4-NANO (measure your board!) ---
board_w    = 65;     // width
board_d    = 30;     // depth
board_h    = 1.6;    // PCB thickness
board_mount_holes = [  // mounting hole positions [x, y] from corner
    [3, 3], [3, 27], [62, 3], [62, 27]
];
board_mount_d = 2.5; // M2.5 mounting hole diameter

// --- RPi Camera Module v2 ---
cam_w      = 25;
cam_d      = 24;
cam_mount_spacing_x = 21;   // hole-to-hole X
cam_mount_spacing_y = 12.5; // hole-to-hole Y
cam_mount_d = 2.2;          // M2 hole diameter

// --- General ---
wall       = 2.5;    // wall thickness
tol        = 0.3;    // printer tolerance (gap for press-fit)
$fn        = 32;     // circle resolution

// ============================================================
// Derived dimensions
// ============================================================
base_w = max(board_w, sg90_flange_w) + wall * 2 + 10;
base_d = board_d + sg90_d + wall * 3 + 5;
base_h = 10;

bracket_w = sg90_flange_w + wall * 2;
bracket_h = sg90_h + sg90_flange_h + wall;
bracket_d = sg90_d + wall * 2;

head_w = cam_w + wall * 2 + 4;
head_d = cam_d + wall * 2 + 4;
head_h = wall + 5;

// ============================================================
// Part 1: Base
// ============================================================
module base() {
    difference() {
        // Main body
        rounded_box(base_w, base_d, base_h, 3);

        // Pan servo cutout (center of base)
        translate([base_w/2 - sg90_w/2 - tol,
                   base_d/2 - sg90_d/2 - tol,
                   wall])
            cube([sg90_w + tol*2, sg90_d + tol*2, base_h]);

        // Servo flange slots
        translate([base_w/2 - sg90_flange_w/2 - tol,
                   base_d/2 - sg90_d/2 - tol,
                   wall + sg90_flange_z - sg90_flange_h])
            cube([sg90_flange_w + tol*2, sg90_d + tol*2, sg90_flange_h + tol]);

        // Shaft hole (top)
        translate([base_w/2 - sg90_w/2 + sg90_shaft_offset,
                   base_d/2,
                   0])
            cylinder(d=10, h=base_h + 1);

        // Board mounting holes
        for (h = board_mount_holes) {
            translate([wall + 2 + h[0],
                       wall + 2 + h[1],
                       0])
                cylinder(d=board_mount_d, h=base_h + 1);
        }

        // Cable routing hole
        translate([base_w/2, base_d - wall - 3, 0])
            cylinder(d=8, h=base_h + 1);
    }

    // Board standoffs
    for (h = board_mount_holes) {
        translate([wall + 2 + h[0],
                   wall + 2 + h[1],
                   0])
            difference() {
                cylinder(d=board_mount_d + 3, h=wall + 3);
                cylinder(d=board_mount_d, h=wall + 4);
            }
    }
}

// ============================================================
// Part 2: Tilt Bracket
// ============================================================
module tilt_bracket() {
    difference() {
        union() {
            // Vertical plate
            rounded_box(bracket_w, wall + 2, bracket_h, 2);

            // Servo holder (horizontal)
            translate([bracket_w/2 - sg90_d/2 - wall,
                       0,
                       bracket_h - sg90_w - wall])
                cube([sg90_d + wall * 2, sg90_w/2 + wall + 5, sg90_w + wall]);
        }

        // Servo cutout
        translate([bracket_w/2 - sg90_d/2 - tol,
                   wall,
                   bracket_h - sg90_w - tol])
            cube([sg90_d + tol*2, sg90_w + 10, sg90_w + tol*2]);

        // Servo flange slot
        translate([bracket_w/2 - sg90_d/2 - tol - (sg90_flange_w - sg90_d)/2,
                   wall,
                   bracket_h - sg90_w - tol + sg90_flange_z - sg90_flange_h])
            cube([sg90_flange_w + tol*2, sg90_w + 10, sg90_flange_h + tol]);

        // Shaft hole
        translate([bracket_w/2,
                   -1,
                   bracket_h - sg90_w + sg90_shaft_offset])
            rotate([-90, 0, 0])
                cylinder(d=10, h=wall + 10);

        // Horn mount hole (bottom, for pan servo horn screw)
        translate([bracket_w/2, (wall + 2)/2, 0])
            cylinder(d=2.2, h=5);

        // Horn screw holes (circular pattern)
        for (a = [0, 90, 180, 270]) {
            translate([bracket_w/2 + cos(a) * 7,
                       (wall + 2)/2 + sin(a) * 7,
                       0])
                cylinder(d=1.5, h=5);
        }
    }
}

// ============================================================
// Part 3: Head (camera mount)
// ============================================================
module head() {
    difference() {
        union() {
            // Main plate
            rounded_box(head_w, head_d, head_h, 2);

            // Horn mount boss (attaches to tilt servo)
            translate([head_w/2, 0, head_h/2])
                rotate([-90, 0, 0])
                    cylinder(d=14, h=wall + 2);
        }

        // Camera mounting holes
        cx = head_w / 2;
        cy = head_d / 2 + 2;
        for (dx = [-1, 1], dy = [-1, 1]) {
            translate([cx + dx * cam_mount_spacing_x/2,
                       cy + dy * cam_mount_spacing_y/2,
                       0])
                cylinder(d=cam_mount_d, h=head_h + 1);
        }

        // Camera lens hole
        translate([cx, cy, 0])
            cylinder(d=10, h=head_h + 1);

        // FFC cable slot
        translate([cx - 8, head_d - wall, 0])
            cube([16, wall + 1, head_h + 1]);

        // Horn center screw hole
        translate([head_w/2, 0, head_h/2])
            rotate([-90, 0, 0])
                cylinder(d=2.2, h=wall + 3);

        // Horn screw holes
        for (a = [0, 90, 180, 270]) {
            translate([head_w/2 + cos(a) * 7,
                       0,
                       head_h/2 + sin(a) * 7])
                rotate([-90, 0, 0])
                    cylinder(d=1.5, h=wall + 3);
        }
    }
}

// ============================================================
// Utility modules
// ============================================================
module rounded_box(w, d, h, r) {
    hull() {
        for (x = [r, w-r], y = [r, d-r]) {
            translate([x, y, 0])
                cylinder(r=r, h=h);
        }
    }
}

// ============================================================
// Render
// ============================================================
if (part == 0) {
    // Assembly view
    color("DimGray") base();

    translate([base_w/2 - bracket_w/2,
               base_d/2 - (wall + 2)/2,
               base_h + 2])
        color("SteelBlue") tilt_bracket();

    translate([base_w/2 - head_w/2,
               base_d/2 - 15,
               base_h + bracket_h + 4])
        color("Coral") head();

} else if (part == 1) {
    base();
} else if (part == 2) {
    tilt_bracket();
} else if (part == 3) {
    head();
}
