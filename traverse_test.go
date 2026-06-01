package dawg_test

import (
	"testing"

	dawg "github.com/iliadenisov/dafsa"
)

// ix maps an ASCII lowercase letter to its alphabet.Latin() index.
func ix(r byte) byte { return r - 'a' }

// walk follows word from start, failing the test if any transition is missing, and
// returns the final state reached and whether it is accepting.
func walk(t *testing.T, c *dawg.Cursor, start dawg.Node, word string) (dawg.Node, bool) {
	t.Helper()
	n := start
	var final bool
	for i := 0; i < len(word); i++ {
		var ok bool
		n, final, ok = c.Next(n, ix(word[i]))
		if !ok {
			t.Fatalf("Next on %q at %d (%c) not found", word, i, word[i])
		}
	}
	return n, final
}

func TestCursorWalk(t *testing.T) {
	finder := createDawg(t, []string{"cat", "cats", "do", "dog", "dogs"})
	c, err := dawg.NewCursor(finder)
	if err != nil {
		t.Fatal(err)
	}
	root := c.Root()

	// "c" alone is neither a word nor final.
	nC, finalC, ok := c.Next(root, ix('c'))
	if !ok || finalC {
		t.Fatalf("Next root->c: ok=%v final=%v, want ok=true final=false", ok, finalC)
	}
	if c.Final(nC) {
		t.Errorf("Final(c) = true, want false")
	}

	// "cat" is a word.
	nCat, finalCat := walk(t, c, root, "cat")
	if !finalCat || !c.Final(nCat) {
		t.Errorf("cat: Next.final=%v Final=%v, want both true", finalCat, c.Final(nCat))
	}

	// "cats" is a word.
	nCats, finalCats, ok := c.Next(nCat, ix('s'))
	if !ok || !finalCats || !c.Final(nCats) {
		t.Errorf("Next cat->s: ok=%v final=%v Final=%v, want all true", ok, finalCats, c.Final(nCats))
	}

	// No edge cat->x.
	if _, _, ok := c.Next(nCat, ix('x')); ok {
		t.Errorf("Next cat->x: ok=true, want false")
	}

	// Root has exactly the edges c and d, in order.
	var labels []byte
	c.Arcs(root, func(a dawg.Arc) bool { labels = append(labels, a.Label); return true })
	if len(labels) != 2 || labels[0] != ix('c') || labels[1] != ix('d') {
		t.Errorf("Arcs(root) labels = %v, want [%d %d]", labels, ix('c'), ix('d'))
	}

	// "do" is a word; its only out-edge is g, leading to the accepting "dog".
	nDo, finalDo := walk(t, c, root, "do")
	if !finalDo {
		t.Errorf("do: final=false, want true")
	}
	var got dawg.Arc
	count := 0
	c.Arcs(nDo, func(a dawg.Arc) bool { got = a; count++; return true })
	if count != 1 || got.Label != ix('g') || !got.Final {
		t.Errorf("Arcs(do) = %+v count=%d, want single g with Final=true", got, count)
	}

	// fn returning false stops enumeration early.
	seen := 0
	c.Arcs(root, func(a dawg.Arc) bool { seen++; return false })
	if seen != 1 {
		t.Errorf("Arcs early-stop visited %d, want 1", seen)
	}
}

func TestCursorEmptyWord(t *testing.T) {
	finder := createDawg(t, []string{"", "a"})
	c, err := dawg.NewCursor(finder)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Final(c.Root()) {
		t.Errorf("Final(root) = false with the empty word added, want true")
	}
	if nA, finalA, ok := c.Next(c.Root(), ix('a')); !ok || !finalA || !c.Final(nA) {
		t.Errorf("Next root->a: ok=%v final=%v, want both true", ok, finalA)
	}
}
