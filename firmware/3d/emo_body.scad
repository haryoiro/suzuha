// ============================================================
// suzuha physical agent — EMO-style desktop body
// ============================================================
// EMO風の小型デスクトップロボット筐体
//
// 構成:
//   - 本体 (角丸の箱型、前面にLCD窓+カメラ穴)
//   - ベースプレート (底面、ネジ止め)
//
// Set `part` variable:
//   0 = assembly view
//   1 = body (top shell)
//   2 = base plate (bottom)
//   3 = face plate (front panel, optional separate print)

part = 0;

// ============================================================
// Parameters
// ============================================================

// --- Overall body ---
body_w     = 62;     // width (mm)
body_d     = 55;     // depth
body_h     = 72;     // height
body_r     = 8;      // corner radius
wall       = 2.0;    // wall thickness

// --- LCD Display ---
// Waveshare 2.8inch DSI LCD or similar small round/square display
// Adjust to your display's active area
lcd_w      = 43;     // visible width
lcd_h      = 43;     // visible height
lcd_r      = 5;      // corner radius of LCD cutout
lcd_offset_y = 8;    // offset from center (push up a bit, like EMO)

// --- Camera ---
cam_hole_d = 8;      // camera lens hole diameter
cam_x      = 0;      // X offset from center (0=centered)
cam_y      = -lcd_h/2 - 8;  // below LCD

// --- Microphone holes ---
mic_hole_d = 2.0;
mic_spacing = 30;    // distance between L/R mics (for INMP441 x2)

// --- Speaker ---
speaker_holes_area_w = 20;  // speaker grille area
speaker_holes_area_h = 8;
speaker_hole_d = 2.0;
speaker_hole_spacing = 3.0;

// --- ESP32-P4-NANO mount ---
board_w    = 65;     // will be mounted vertically or folded inside
board_d    = 30;
mount_d    = 2.5;    // M2.5

// --- Base ---
base_h     = 3;      // base plate thickness
screw_d    = 2.5;    // M2.5 screws to join body + base

// --- Misc ---
tol        = 0.3;
$fn        = 48;

// ============================================================
// Modules
// ============================================================

module rounded_box(w, d, h, r) {
    hull() {
        for (x = [r, w-r], y = [r, d-r]) {
            translate([x, y, 0])
                cylinder(r=r, h=h);
        }
    }
}

module rounded_rect(w, h, r) {
    hull() {
        for (x = [r, w-r], y = [r, h-r]) {
            translate([x, y])
                circle(r=r);
        }
    }
}

// ============================================================
// Part 1: Body (top shell)
// ============================================================
module body() {
    cx = body_w / 2;
    cy = body_d / 2;
    face_z = body_h * 0.55;  // face center height

    difference() {
        // Outer shell
        rounded_box(body_w, body_d, body_h, body_r);

        // Hollow inside (open bottom)
        translate([wall, wall, -0.1])
            rounded_box(body_w - wall*2, body_d - wall*2, body_h - wall + 0.1, body_r - wall);

        // --- Front face cutouts ---

        // LCD window
        translate([cx - lcd_w/2, -1, face_z - lcd_h/2 + lcd_offset_y])
            rotate([-90, 0, 0])
                linear_extrude(wall + 2)
                    rounded_rect(lcd_w, lcd_h, lcd_r);

        // Camera hole (below LCD)
        translate([cx + cam_x, -1, face_z + cam_y])
            rotate([-90, 0, 0])
                cylinder(d=cam_hole_d, h=wall + 2);

        // --- Microphone holes (front, flanking camera) ---
        for (side = [-1, 1]) {
            translate([cx + side * mic_spacing/2, -1, face_z + cam_y])
                rotate([-90, 0, 0])
                    cylinder(d=mic_hole_d, h=wall + 2);
        }

        // --- Speaker grille (back) ---
        translate([0, body_d - wall - 1, 0])
        {
            cols = floor(speaker_holes_area_w / speaker_hole_spacing);
            rows = floor(speaker_holes_area_h / speaker_hole_spacing);
            for (c = [0:cols-1], r = [0:rows-1]) {
                translate([
                    cx - speaker_holes_area_w/2 + c * speaker_hole_spacing + speaker_hole_spacing/2,
                    0,
                    body_h * 0.25 + r * speaker_hole_spacing
                ])
                    rotate([-90, 0, 0])
                        cylinder(d=speaker_hole_d, h=wall + 2);
            }
        }

        // --- USB-C access hole (back, bottom) ---
        translate([cx - 5, body_d - wall - 1, wall + 2])
            cube([10, wall + 2, 8]);

        // --- Base screw holes (from bottom) ---
        for (pos = base_screw_positions()) {
            translate([pos[0], pos[1], 0])
                cylinder(d=screw_d, h=wall + 5);
        }
    }

    // --- Internal LCD mount posts ---
    // 4 posts around LCD window to hold display
    translate([0, 0, 0])
    for (dx = [-1, 1], dz = [-1, 1]) {
        px = cx + dx * (lcd_w/2 + 3);
        pz = face_z + lcd_offset_y + dz * (lcd_h/2 + 3);
        if (px > wall + 3 && px < body_w - wall - 3 &&
            pz > wall + 3 && pz < body_h - wall - 3)
        {
            translate([px, wall, pz])
                rotate([-90, 0, 0])
                    difference() {
                        cylinder(d=5, h=6);
                        cylinder(d=1.8, h=7);  // M2 screw hole
                    }
        }
    }
}

function base_screw_positions() = [
    [body_r + 2, body_r + 2],
    [body_w - body_r - 2, body_r + 2],
    [body_r + 2, body_d - body_r - 2],
    [body_w - body_r - 2, body_d - body_r - 2],
];

// ============================================================
// Part 2: Base plate
// ============================================================
module base_plate() {
    difference() {
        rounded_box(body_w, body_d, base_h, body_r);

        // Screw holes
        for (pos = base_screw_positions()) {
            translate([pos[0], pos[1], 0])
                cylinder(d=screw_d, h=base_h + 1);
        }

        // Ventilation slots (bottom)
        cx = body_w / 2;
        for (i = [-2:2]) {
            translate([cx + i * 8 - 2, body_d * 0.3, -0.1])
                cube([4, body_d * 0.4, base_h + 0.2]);
        }
    }

    // Board standoffs
    // ESP32-P4-NANO mounted flat on base
    board_ox = (body_w - board_d) / 2;  // board rotated 90deg to fit
    board_oy = (body_d - board_w) / 2 + 5;
    standoff_h = 4;

    for (dx = [3, board_d - 3], dy = [3, board_w - 3]) {
        bx = board_ox + dx;
        by = board_oy + dy;
        if (bx > body_r && bx < body_w - body_r &&
            by > body_r && by < body_d - body_r)
        {
            translate([bx, by, base_h])
                difference() {
                    cylinder(d=mount_d + 3, h=standoff_h);
                    cylinder(d=mount_d, h=standoff_h + 1);
                }
        }
    }
}

// ============================================================
// Render
// ============================================================
if (part == 0) {
    // Assembly view
    color("DimGray", 0.8) body();
    color("SlateGray") base_plate();
} else if (part == 1) {
    body();
} else if (part == 2) {
    base_plate();
}
