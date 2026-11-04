//go:build darwin

package e2e

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// TestAccessGrid reads, as VoiceOver does, a spreadsheet of 10,000 rows
// built only where it shows: a table whose size is in AXARIARowCount and
// AXARIAColumnCount, and AXRowCount, whose rows and cells say their
// places in AXARIARowIndex and AXARIAColumnIndex, as WebKit's grids do;
// a cell without a column of its own names no AXARIAColumnIndex.
func TestAccessGrid(t *testing.T) {
	var frames atomic.Int32
	cell := func(c *ui.Context, name string) ui.Element {
		return ui.Box(c).Role(ui.RoleCell).Label(name).Padding(2).Children(func() { ui.Text(c, name) })
	}
	view := func(c *ui.Context) {
		frames.Add(1)
		ui.Column(c).Fill().Padding(20).Gap(10).Children(func() {
			ui.Column(c).Role(ui.RoleGrid).Label("Sheet").RowCount(10001).ColumnCount(3).Multiselectable(true).Children(func() {
				for r := 4120; r < 4122; r++ {
					ui.Row(c).Role(ui.RoleRow).RowIndex(r + 1).Selected(r == 4121).Children(func() {
						cell(c, fmt.Sprintf("A%d", r))
						cell(c, fmt.Sprintf("C%d", r)).ColumnIndex(3)
					})
				}
			})
			ui.Column(c).Role(ui.RoleTreeGrid).Label("Outline").Children(func() {
				ui.Row(c).Role(ui.RoleRow).Level(1).Children(func() { cell(c, "src") })
			})
		})
	}
	w := newWindow(t, mygo.WindowOptions{Title: "Grid", Width: 400, Height: 300, Content: ui.View(view)})
	eventually(t, "a frame", func() bool { return frames.Load() > 0 })
	for _, c := range []struct{ label, attr, want string }{
		{"Sheet", "AXRole", "AXTable"},
		{"Sheet", "AXARIARowCount", "10001"},
		{"Sheet", "AXARIAColumnCount", "3"},
		{"Sheet", "AXRowCount", "10001"},
		{"A4121 C4121", "AXARIARowIndex", "4122"},
		{"A4121 C4121", "AXIndex", "4121"},
		{"C4121", "AXARIAColumnIndex", "3"},
		{"C4121", "AXARIARowIndex", "4122"},
		{"A4121", "AXARIARowIndex", "4122"},
		{"Outline", "AXRole", "AXTable"},
	} {
		eventually(t, c.label+"'s "+c.attr, func() bool {
			v, _, named, ok := axAttribute(w, c.label, c.attr)
			return ok && (named || c.attr == "AXRole" || c.attr == "AXRowCount" || c.attr == "AXIndex") && v == c.want
		})
	}
	if v, _, named, ok := axAttribute(w, "A4121", "AXARIAColumnIndex"); !ok || named {
		t.Errorf("A4121's AXARIAColumnIndex %q, named %v, found %v", v, named, ok)
	}
}
