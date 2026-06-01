// This file adds a low-level, step-by-step traversal API on top of the finished
// DAWG. The public Finder methods are word-oriented (FindAllPrefixesOf, IndexOf,
// Enumerate); they cannot drive the automaton one transition at a time, which is
// what a word-puzzle move generator (and similar backtracking searches over the
// graph) needs. The primitives here expose exactly that, reusing the same on-disk
// format and bit reader as the rest of the package.
package dawg

import (
	"fmt"
	"io"
	"math/bits"
)

// Node identifies a state in a finished DAWG. It is an opaque handle valid only for
// the Finder (and Cursor) it was obtained from; do not persist it or mix it across
// Finders. The zero value is the root state.
type Node struct {
	// off is the node's bit offset in the serialized graph; 0 denotes the root.
	off int
}

// IsRoot reports whether n is the root state.
func (n Node) IsRoot() bool { return n.off == rootNode }

// Arc is a single labelled out-edge of a state.
type Arc struct {
	// Label is the alphabet index labelling the edge.
	Label byte
	// Dest is the state the edge leads to.
	Dest Node
	// Final reports whether Dest is an accepting state (a stored word ends there).
	Final bool
}

// Cursor walks a finished DAWG one transition at a time without allocating per step.
// It carries a private bit reader with mutable position, so a single Cursor must not
// be used from multiple goroutines concurrently; create one Cursor per goroutine with
// NewCursor. Distinct Cursors over the same Finder may be used concurrently, because
// the underlying graph is read-only.
type Cursor struct {
	d *dawg
	r bitSeeker
}

// NewCursor returns a Cursor over f, which must be a Finder produced by Finish, Load
// or Read. It returns an error if f is not such a value.
func NewCursor(f Finder) (*Cursor, error) {
	d, ok := f.(*dawg)
	if !ok {
		return nil, fmt.Errorf("dawg: NewCursor requires a Finder returned by Finish/Load/Read, got %T", f)
	}
	return &Cursor{d: d, r: newBitSeeker(d.r)}, nil
}

// Root returns the start state of the automaton.
func (c *Cursor) Root() Node { return Node{off: rootNode} }

// Final reports whether n is an accepting state.
func (c *Cursor) Final(n Node) bool {
	if c.d.numEdges == 0 {
		return c.d.hasEmptyWord && n.off == rootNode
	}
	pos := int64(n.off)
	if pos == 0 {
		pos = c.d.firstNodeOffset
	}
	c.r.Seek(pos, io.SeekStart)
	return c.r.ReadBits(1) == 1
}

// Next follows the edge labelled ch (an alphabet index) from n. It returns the
// destination state, whether that state is accepting, and whether such an edge
// exists. When ok is false the other results are meaningless.
func (c *Cursor) Next(n Node, ch byte) (dest Node, final, ok bool) {
	ee, fin, found := c.d.getEdge(&c.r, edgeStart{node: n.off, ch: ch})
	if !found {
		return Node{}, false, false
	}
	return Node{off: ee.node}, fin, true
}

// Arcs calls fn for each out-edge of n in ascending label order, stopping early if fn
// returns false. It allocates nothing.
func (c *Cursor) Arcs(n Node, fn func(Arc) bool) {
	d := c.d
	if d.numEdges == 0 {
		return
	}
	r := &c.r

	pos := int64(n.off)
	if pos == 0 {
		pos = d.firstNodeOffset
	}
	r.Seek(pos, io.SeekStart)

	r.ReadBits(1) // node final flag (not needed here)
	fallthr := r.ReadBits(1)

	if fallthr == 1 {
		ch := byte(r.ReadBits(d.cbits))
		// The reader is now positioned exactly at the destination node, whose first
		// bit is its final flag.
		child := int(r.Tell())
		final := r.ReadBits(1) == 1
		fn(Arc{Label: ch, Dest: Node{off: child}, Final: final})
		return
	}

	nskiplen := int64(bits.Len(uint(d.wbits)))
	nskip := int64(0)
	numEdges := uint64(1)
	if r.ReadBits(1) != 1 { // not a single edge
		numEdges = readUnsigned(r)
		nskip = int64(r.ReadBits(nskiplen))
	}

	for i := uint64(0); i < numEdges; i++ {
		ch := byte(r.ReadBits(d.cbits))
		if i > 0 {
			r.ReadBits(nskip) // per-edge skip count, unused for traversal
		}
		addr := int(r.ReadBits(d.abits))
		resume := r.Tell()

		r.Seek(int64(addr), io.SeekStart)
		final := r.ReadBits(1) == 1
		if !fn(Arc{Label: ch, Dest: Node{off: addr}, Final: final}) {
			return
		}
		r.Seek(resume, io.SeekStart)
	}
}
