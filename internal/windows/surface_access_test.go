//go:build windows && (amd64 || arm64)

package windows

import (
	"slices"
	"testing"
	"unsafe"

	"github.com/egoist/mygo/internal/platform"
)

// accessTree returns a tree of a list of rows, each with a text, as the
// content describes a frame: rows are the IDs of the rows in order.
func accessTree(rows ...uint64) *platform.AccessTree {
	t := &platform.AccessTree{Nodes: []platform.AccessNode{{ID: 1, Parent: -1, Role: platform.RoleList}}}
	for _, id := range rows {
		t.Nodes = append(t.Nodes, platform.AccessNode{ID: id, Parent: 0, Role: platform.RoleListItem, Label: "row"})
		t.Nodes = append(t.Nodes, platform.AccessNode{ID: id << 8, Parent: len(t.Nodes) - 1, Role: platform.RoleText, Label: "text"})
	}
	return t
}

// newTestTree returns a tree as accessRoot makes it, without a window.
func newTestTree(t testing.TB, clients bool) *uiaTree {
	uiaOnce.Do(initUIA)
	saved := listening
	listening = func() bool { return clients }
	t.Cleanup(func() { listening = saved })
	tree := &uiaTree{nodes: map[uint64]*uiaElement{}}
	tree.root = newUIAElement(tree)
	tree.root.root = true
	return tree
}

func ids(es []*uiaElement) []uint64 {
	var out []uint64
	for _, e := range es {
		out = append(out, e.n.ID)
	}
	return out
}

// The elements of the tree follow the content's trees as it reuses the
// buffers of the last ones: rows coming, going and moving.
func TestUIATreeUpdate(t *testing.T) {
	tree := newTestTree(t, false)
	frames := [][]uint64{{2, 3, 4}, {2, 3, 4}, {3, 4, 5, 6}, {6, 3}, {6, 3}, {}, {7, 2}}
	var list *uiaElement
	for i, rows := range frames {
		tree.update(accessTree(rows...))
		if got := ids(tree.root.children); !slices.Equal(got, []uint64{1}) {
			t.Fatalf("frame %d: the root's children %v", i, got)
		}
		l := tree.nodes[1]
		if i > 0 && l != list {
			t.Fatalf("frame %d: the list is a new element", i)
		}
		list = l
		if got := ids(l.children); !slices.Equal(got, rows) {
			t.Fatalf("frame %d: the list's children %v, want %v", i, got, rows)
		}
		for _, row := range l.children {
			if row.parent != l || len(row.children) != 1 || row.children[0].n.ID != row.n.ID<<8 || row.children[0].parent != row {
				t.Fatalf("frame %d: row %d's links", i, row.n.ID)
			}
		}
		if len(tree.nodes) != 1+2*len(rows) || len(tree.order) != len(tree.nodes) {
			t.Fatalf("frame %d: %d nodes, %d in order", i, len(tree.nodes), len(tree.order))
		}
	}
	// Rows that left were let go of: none of the spare buffers holds them.
	for _, e := range tree.spareOrder[:cap(tree.spareOrder)] {
		if e != nil {
			t.Fatal("the spare order keeps an element")
		}
	}
	if len(tree.spareNodes) != 0 {
		t.Fatal("the spare map keeps elements")
	}
}

// The nodes with the announcer are let go of once the elements have them,
// as the strings of a tree that shrank would be kept past its length.
func TestUIATreeAnnouncer(t *testing.T) {
	tree := newTestTree(t, false)
	frame := accessTree(2, 3, 4)
	frame.Announcements = []string{"saved"}
	tree.update(frame)
	tree.update(accessTree(2))
	if e := tree.nodes[announcerID]; e == nil || e.n.Label != "saved" {
		t.Fatal("the announcer left the tree")
	}
	if got := ids(tree.root.children); !slices.Equal(got, []uint64{1, announcerID}) {
		t.Fatalf("the root's children %v", got)
	}
	for _, n := range tree.withAnnouncer[:cap(tree.withAnnouncer)] {
		if n.Label != "" {
			t.Fatalf("the nodes with the announcer keep %q", n.Label)
		}
	}
	if tree.announcerTree.Nodes != nil || tree.announcerTree.Announcements != nil {
		t.Fatal("the tree with the announcer is kept")
	}
}

func TestChildChanges(t *testing.T) {
	e := func(id uint64) *uiaElement { return &uiaElement{n: platform.AccessNode{ID: id}} }
	a, b, c, d := e(1), e(2), e(3), e(4)
	added, removed := childChanges([]*uiaElement{a, b, c}, []*uiaElement{b, d, a}, nil, nil)
	if !slices.Equal(ids(added), []uint64{4}) || !slices.Equal(ids(removed), []uint64{3}) {
		t.Errorf("added %v, removed %v", ids(added), ids(removed))
	}
	added, removed = childChanges([]*uiaElement{a}, []*uiaElement{a}, added, removed)
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("unchanged children: added %v, removed %v", ids(added), ids(removed))
	}
}

// A frame like the last allocates nothing, whether clients listen or not:
// the content describes every frame once one asked for the window.
func TestUIATreeUpdateAllocations(t *testing.T) {
	for _, clients := range []bool{false, true} {
		tree := newTestTree(t, clients)
		frame := accessTree(2, 3, 4, 5, 6, 7, 8, 9)
		// The first tree, as a client asks for it, raises no events: they
		// would reach UI Automation, which has no window here.
		tree.quiet = true
		tree.update(frame)
		tree.quiet = false
		tree.update(frame)
		if n := testing.AllocsPerRun(20, func() { tree.update(frame) }); n != 0 {
			t.Errorf("clients listening %v: %v allocations a frame", clients, n)
		}
	}
}

func BenchmarkUIATreeUpdate(b *testing.B) {
	rows := make([]uint64, 400)
	for i := range rows {
		rows[i] = uint64(i + 2)
	}
	frame := accessTree(rows...)
	for _, clients := range []bool{false, true} {
		name := "quiet"
		if clients {
			name = "listening"
		}
		b.Run(name, func(b *testing.B) {
			tree := newTestTree(b, clients)
			tree.quiet = true
			tree.update(frame)
			tree.quiet = false
			b.ReportAllocs()
			for b.Loop() {
				tree.update(frame)
			}
		})
	}
}

// A range with a text for its value (aria-valuetext) gives it through the
// Value pattern beside RangeValue; one without has RangeValue alone.
func TestUIARangeValueText(t *testing.T) {
	for _, c := range []struct {
		value string
		want  bool
	}{{"30 percent", true}, {"", false}} {
		e := &uiaElement{n: platform.AccessNode{Role: platform.RoleSlider, Max: 100, Now: 30, Value: c.value}}
		if !e.supports(ifaceRangeValue) {
			t.Errorf("value %q: no RangeValue", c.value)
		}
		if got := e.supports(ifaceValue); got != c.want {
			t.Errorf("value %q: Value pattern %v, want %v", c.value, got, c.want)
		}
	}
}

// A grid built only where it shows gives Narrator its size and its
// cells' places as the app gave them, a table as its rows say which of
// all they are after its header: the Grid, GridItem, Table and TableItem
// patterns, through their vtables as UI Automation calls them.
func TestUIAGrid(t *testing.T) {
	tree := newTestTree(t, false)
	grid := &platform.AccessTree{Nodes: []platform.AccessNode{
		{ID: 1, Parent: -1, Role: platform.RoleGrid, Label: "Sheet", RowCount: 10001, ColCount: 3, States: platform.AccessSelectable | platform.AccessMultiselectable},
		{ID: 2, Parent: 0, Role: platform.RoleRow, RowIndex: 1},
		{ID: 3, Parent: 1, Role: platform.RoleColumnHeader, Label: "A"},
		{ID: 4, Parent: 1, Role: platform.RoleColumnHeader, Label: "B"},
		{ID: 5, Parent: 1, Role: platform.RoleColumnHeader, Label: "C"},
		{ID: 6, Parent: 0, Role: platform.RoleRow, RowIndex: 4122, States: platform.AccessSelectable | platform.AccessChecked},
		{ID: 7, Parent: 5, Role: platform.RoleCell, Label: "A4121"},
		{ID: 8, Parent: 5, Role: platform.RoleCell, Label: "C4121", ColIndex: 3},
	}}
	tree.update(grid)
	g, b, c := tree.nodes[1], tree.nodes[4], tree.nodes[8]
	for i, want := range map[int]bool{ifaceGrid: true, ifaceTable: true, ifaceSelection: true, ifaceGridItem: false} {
		if g.supports(i) != want {
			t.Errorf("the grid supports interface %d: %v", i, !want)
		}
	}
	if !c.supports(ifaceGridItem) || !c.supports(ifaceTableItem) || tree.nodes[6].supports(ifaceGridItem) {
		t.Error("cells and rows have the wrong patterns")
	}
	var v variant
	g.property(uiaControlTypeProperty, &v)
	if v.Val != 50028 {
		t.Errorf("the grid's control type %d", v.Val)
	}
	get := func(e *uiaElement, iface, method int, args ...uintptr) int32 {
		var n int32
		if hr := comCall(e.ptr(iface), method, append(args, uintptr(unsafe.Pointer(&n)))...); hr != sOK {
			t.Errorf("method %d of interface %d: %#x", method, iface, hr)
		}
		return n
	}
	// IUnknown's three methods come first.
	if rows, cols := get(g, ifaceGrid, 4), get(g, ifaceGrid, 5); rows != 10001 || cols != 3 {
		t.Errorf("the grid has %d rows and %d columns", rows, cols)
	}
	if row, col := get(c, ifaceGridItem, 3), get(c, ifaceGridItem, 4); row != 4121 || col != 2 {
		t.Errorf("C4121 is at row %d, column %d", row, col)
	}
	if row, col := get(b, ifaceGridItem, 3), get(b, ifaceGridItem, 4); row != 0 || col != 1 {
		t.Errorf("B is at row %d, column %d", row, col)
	}
	var item uintptr
	if hr := comCall(g.ptr(ifaceGrid), 3, 4121, 0, uintptr(unsafe.Pointer(&item))); hr != sOK || item != tree.nodes[7].ptr(ifaceSimple) {
		t.Errorf("GetItem(4121, 0): %#x, %#x", hr, item)
	}
	tree.nodes[7].release()
	if hr := comCall(g.ptr(ifaceGrid), 3, 9, 0, uintptr(unsafe.Pointer(&item))); hr != sOK || item != 0 {
		t.Errorf("GetItem of a row not built: %#x, %#x", hr, item)
	}
	if hr := comCall(g.ptr(ifaceGrid), 3, 10001, 0, uintptr(unsafe.Pointer(&item))); hr != eInvalidArg {
		t.Errorf("GetItem past the last row: %#x", hr)
	}
	if heads := ids(g.columnHeaders(-1)); !slices.Equal(heads, []uint64{3, 4, 5}) {
		t.Errorf("the column headers %v", heads)
	}
	if heads := ids(g.columnHeaders(2)); !slices.Equal(heads, []uint64{5}) {
		t.Errorf("C4121's column header %v", heads)
	}
	if chosen := ids(g.chosen()); !slices.Equal(chosen, []uint64{6}) {
		t.Errorf("the rows chosen %v", chosen)
	}

	// A table whose rows say which of all they are, below its header.
	tree.update(&platform.AccessTree{Nodes: []platform.AccessNode{
		{ID: 10, Parent: -1, Role: platform.RoleTable, SetSize: 500},
		{ID: 11, Parent: 0, Role: platform.RoleRow},
		{ID: 12, Parent: 1, Role: platform.RoleColumnHeader, Label: "Name"},
		{ID: 13, Parent: 1, Role: platform.RoleColumnHeader, Label: "Size"},
		{ID: 14, Parent: 0, Role: platform.RoleRow, PosInSet: 41, SetSize: 500},
		{ID: 15, Parent: 4, Role: platform.RoleCell, Label: "notes.txt"},
		{ID: 16, Parent: 4, Role: platform.RoleCell, Label: "2 KB"},
	}})
	table := tree.nodes[10]
	if rows, cols := table.rowCount(), table.colCount(); rows != 501 || cols != 2 {
		t.Errorf("the table has %d rows and %d columns", rows, cols)
	}
	if row, col := tree.nodes[16].place(); row != 41 || col != 1 {
		t.Errorf("2 KB is at row %d, column %d", row, col)
	}
	if e := table.cellAt(41, 0); e == nil || e.n.ID != 15 {
		t.Errorf("the cell at row 41, column 0: %v", e)
	}
}
