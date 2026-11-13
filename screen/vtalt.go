package screen

import (
	"reflect"
	"unsafe"

	"github.com/charmbracelet/x/vt"
)

// altScreenOf is x/vt's alternate screen, nil if its layout is not the one
// this was written for (v0.1.0: Emulator.scrs [2]Screen, the alternate at
// index 1).
//
// Why reach inside: x/vt gives each of its two screens a 10 000-line
// scrollback, and its API (Scrollback, ClearScrollback, SetScrollbackSize)
// reaches the main screen's only. A full-screen program that scrolls would
// keep up to 10 000 cell rows there, about 120 MiB at 120 columns, which the
// seam never reads (an alternate screen has no scrollback, spec 4.6). The
// one write is a SetScrollbackSize(1) on that screen, at construction,
// through x/vt's own exported method. TestAltScreenScrollbackIsBounded
// fails if a new x/vt moves the field, instead of the bound silently
// disappearing.
func altScreenOf(e *vt.Emulator) *vt.Screen {
	f := reflect.ValueOf(e).Elem().FieldByName("scrs")
	if !f.IsValid() || f.Kind() != reflect.Array || f.Len() != 2 || f.Type().Elem() != reflect.TypeOf(vt.Screen{}) {
		return nil
	}
	return (*vt.Screen)(unsafe.Pointer(f.Index(1).UnsafeAddr()))
}
