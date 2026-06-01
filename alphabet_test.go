package dawg_test

import (
	"bytes"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/iliadenisov/alphabet"
	dawg "github.com/iliadenisov/dafsa"
)

// TestIndexOrderEnforced verifies that words are ordered by alphabet index, not
// by Unicode code point. In ru, ё has index 6 (between е=5 and ж=7) although its
// code point U+0451 is greater than я (U+044F).
func TestIndexOrderEnforced(t *testing.T) {
	ru := alphabet.Embedded(alphabet.Langs.LangRu)
	if ru == nil {
		t.Fatal("ru alphabet is not embedded")
	}

	// ё after я is decreasing by index, so it must be rejected even though it
	// would be increasing by code point.
	d := dawg.New(ru)
	if err := d.Add("я"); err != nil {
		t.Fatalf("Add(я): %v", err)
	}
	if d.CanAdd("ё") {
		t.Error("CanAdd(ё) after я should be false by index order")
	}
	if err := d.Add("ё"); err == nil {
		t.Error("Add(ё) after я should fail by index order")
	}

	// ё before я is increasing by index and must be accepted.
	d2 := dawg.New(ru)
	if err := d2.Add("ё"); err != nil {
		t.Fatalf("Add(ё): %v", err)
	}
	if !d2.CanAdd("я") {
		t.Error("CanAdd(я) after ё should be true by index order")
	}
	if err := d2.Add("я"); err != nil {
		t.Errorf("Add(я) after ё: %v", err)
	}
}

// sortByIndex returns words sorted and de-duplicated by their alphabet index
// encoding, which is the order the builder requires.
func sortByIndex(t *testing.T, idx alphabet.Indexer, words []string) []string {
	t.Helper()
	out := slices.Clone(words)
	slices.SortFunc(out, func(a, b string) int {
		ea, err := idx.Encode(a)
		if err != nil {
			t.Fatalf("Encode(%q): %v", a, err)
		}
		eb, err := idx.Encode(b)
		if err != nil {
			t.Fatalf("Encode(%q): %v", b, err)
		}
		return bytes.Compare(ea, eb)
	})
	return slices.Compact(out)
}

func TestCyrillicRoundTrip(t *testing.T) {
	ru := alphabet.Embedded(alphabet.Langs.LangRu)
	if ru == nil {
		t.Fatal("ru alphabet is not embedded")
	}

	words := sortByIndex(t, ru, []string{
		"да", "дом", "ёж", "ёжик", "мир", "яблоко", "ящик",
	})

	d := dawg.New(ru)
	for _, w := range words {
		if err := d.Add(w); err != nil {
			t.Fatalf("Add(%q): %v", w, err)
		}
	}
	finder := d.Finish()
	testDawg(t, finder, words)

	// ru is embedded, so the file round-trips through Save/Load.
	path := filepath.Join(t.TempDir(), "ru.dawg")
	if _, err := finder.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := dawg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	testDawg(t, loaded, words)

	// A compact alphabet (≤63 tokens) must encode each character in ≤6 bits.
	hdr, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cbits := hdr[4]; cbits > 6 {
		t.Errorf("cbits=%d, expected ≤6 for a compact alphabet", cbits)
	} else {
		t.Logf("Cyrillic: words=%d nodes=%d edges=%d cbits=%d",
			finder.NumAdded(), finder.NumNodes(), finder.NumEdges(), cbits)
	}
}

func TestOutOfAlphabetErrors(t *testing.T) {
	finder := createDawg(t, []string{"cat", "dog"})

	for _, in := range []string{"Cat", "c4t", "café", "DOG"} {
		if _, err := finder.IndexOf(in); err == nil {
			t.Errorf("IndexOf(%q) should error (out of alphabet)", in)
		}
		if _, err := finder.FindAllPrefixesOf(in); err == nil {
			t.Errorf("FindAllPrefixesOf(%q) should error (out of alphabet)", in)
		}
	}

	// The error should unwrap to alphabet.InputError.
	_, err := finder.IndexOf("Cat")
	var inputErr alphabet.InputError
	if !errors.As(err, &inputErr) {
		t.Errorf("IndexOf error should wrap alphabet.InputError, got %T: %v", err, err)
	}

	// The builder rejects out-of-alphabet words too.
	d := dawg.New(alphabet.Latin())
	if err := d.Add("Hello"); err == nil {
		t.Error("Add(Hello) should error (uppercase out of alphabet)")
	}
	if d.CanAdd("Hello") {
		t.Error("CanAdd(Hello) should be false (out of alphabet)")
	}
}

func TestNotFoundVsError(t *testing.T) {
	finder := createDawg(t, []string{"cat", "dog"})

	// Absent but encodable word: (-1, nil), not an error.
	idx, err := finder.IndexOf("bird")
	if err != nil {
		t.Errorf("IndexOf(bird) unexpected error: %v", err)
	}
	if idx != -1 {
		t.Errorf("IndexOf(bird)=%d, want -1", idx)
	}

	// Present word.
	if idx, err := finder.IndexOf("cat"); err != nil || idx != 0 {
		t.Errorf("IndexOf(cat)=%d,%v want 0,nil", idx, err)
	}

	// Encodable input with no stored prefixes: empty result, no error.
	res, err := finder.FindAllPrefixesOf("bird")
	if err != nil {
		t.Errorf("FindAllPrefixesOf(bird) error: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("FindAllPrefixesOf(bird)=%v, want empty", res)
	}
}

func TestCanAdd(t *testing.T) {
	d := dawg.New(alphabet.Latin())
	if !d.CanAdd("apple") {
		t.Error("CanAdd(apple) on an empty builder should be true")
	}
	if err := d.Add("apple"); err != nil {
		t.Fatal(err)
	}
	if d.CanAdd("apple") {
		t.Error("CanAdd(apple) for a duplicate should be false")
	}
	if d.CanAdd("aardvark") {
		t.Error("CanAdd(aardvark) decreasing should be false")
	}
	if !d.CanAdd("banana") {
		t.Error("CanAdd(banana) increasing should be true")
	}
	if d.CanAdd("Apple") {
		t.Error("CanAdd(Apple) out of alphabet should be false")
	}

	d.Finish()
	if d.CanAdd("zebra") {
		t.Error("CanAdd after Finish should be false")
	}
}

func TestCustomAlphabet(t *testing.T) {
	custom, err := alphabet.New(alphabet.Lang("abccustom"), "a\nb\nc\n")
	if err != nil {
		t.Fatal(err)
	}

	words := []string{"a", "ab", "abc", "b", "c"}
	d := dawg.New(custom)
	for _, w := range words {
		if err := d.Add(w); err != nil {
			t.Fatalf("Add(%q): %v", w, err)
		}
	}
	finder := d.Finish()

	// In-memory queries work for a custom alphabet.
	testDawg(t, finder, words)

	// But Save refuses a non-embedded alphabet, since Load could not
	// reconstruct it from the stored language code.
	path := filepath.Join(t.TempDir(), "custom.dawg")
	if _, err := finder.Save(path); err == nil {
		t.Error("Save with a custom (non-embedded) alphabet should error")
	}
}

// TestLargeAlphabetMinimization builds over a 63-token alphabet (the maximum),
// so edge indexes span 0..62, including values that coincide with the separator
// bytes used by nameOf (':'=58, '!'=33, digits 48..57). If the byte-level node
// naming were ambiguous, minimization would merge distinct nodes and lookups
// would return wrong results.
func TestLargeAlphabetMinimization(t *testing.T) {
	var tokens []rune
	for r := 'a'; r <= 'z'; r++ {
		tokens = append(tokens, r) // 26 Latin
	}
	for r := '0'; r <= '9'; r++ {
		tokens = append(tokens, r) // 10 digits
	}
	for r := 'а'; len(tokens) < 63; r++ {
		tokens = append(tokens, r) // fill to 63 with Cyrillic
	}

	lines := make([]string, len(tokens))
	for i, r := range tokens {
		lines[i] = string(r)
	}
	idx, err := alphabet.New(alphabet.Lang("big63"), strings.Join(lines, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if idx.Size() != 63 {
		t.Fatalf("alphabet size=%d, want 63", idx.Size())
	}

	// A deterministic word set over the token runes.
	rng := rand.New(rand.NewSource(7))
	set := make(map[string]struct{}, 2000)
	for len(set) < 1000 {
		n := 1 + rng.Intn(8)
		rs := make([]rune, n)
		for i := range rs {
			rs[i] = tokens[rng.Intn(len(tokens))]
		}
		set[string(rs)] = struct{}{}
	}
	words := make([]string, 0, len(set))
	for w := range set {
		words = append(words, w)
	}
	words = sortByIndex(t, idx, words)

	d := dawg.New(idx)
	for _, w := range words {
		if err := d.Add(w); err != nil {
			t.Fatalf("Add(%q): %v", w, err)
		}
	}
	finder := d.Finish()

	// Round-trips in memory verify minimization kept every node distinct.
	testDawg(t, finder, words)
}

// TestLoadUnknownLangErrors corrupts the stored language code to a non-embedded
// one and verifies Load reports an error rather than decoding with the wrong
// alphabet.
func TestLoadUnknownLangErrors(t *testing.T) {
	finder := createDawg(t, []string{"cat"})
	path := filepath.Join(t.TempDir(), "en.dawg")
	if _, err := finder.Save(path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Header: 4 bytes size, 1 byte cbits, 1 byte abits, then a 7code length of
	// the language code followed by the code bytes. "en" has length 2 (one
	// byte, < 0x7f), so the code bytes sit at offsets 7 and 8.
	if data[6] != 0x02 || string(data[7:9]) != "en" {
		t.Fatalf("unexpected header: len=0x%02x code=%q", data[6], string(data[7:9]))
	}
	data[7], data[8] = 'z', 'z' // "zz" is not an embedded language
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := dawg.Load(path); err == nil {
		t.Error("Load of a file with an unknown language code should error")
	}
}

// TestByteAPIEquivalence checks that the *B methods agree with their string
// counterparts and that AddB builds the same graph as Add.
func TestByteAPIEquivalence(t *testing.T) {
	idx := alphabet.Latin()
	words := []string{"", "blip", "cat", "catnip", "cats"}

	// Build the same dawg through AddB.
	db := dawg.New(idx)
	for _, w := range words {
		enc, err := idx.Encode(w)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.AddB(enc); err != nil {
			t.Fatalf("AddB(%q): %v", w, err)
		}
	}
	finder := db.Finish()

	// Same shape as the string-built dawg.
	strBuilt := createDawg(t, words)
	if finder.NumNodes() != strBuilt.NumNodes() ||
		finder.NumEdges() != strBuilt.NumEdges() ||
		finder.NumAdded() != strBuilt.NumAdded() {
		t.Errorf("byte-built dawg shape differs from string-built one")
	}

	for i, w := range words {
		enc, _ := idx.Encode(w)

		si, err := finder.IndexOf(w)
		if err != nil {
			t.Fatal(err)
		}
		if bi := finder.IndexOfB(enc); si != i || bi != i {
			t.Errorf("IndexOf/IndexOfB(%q)=%d/%d, want %d", w, si, bi, i)
		}

		s, _ := finder.AtIndex(i)
		b, _ := finder.AtIndexB(i)
		dec, _ := idx.Decode(b)
		if s != w || dec != w {
			t.Errorf("AtIndex/AtIndexB(%d)=%q/%q, want %q", i, s, dec, w)
		}
	}

	// FindAllPrefixesOf vs FindAllPrefixesOfB.
	enc, _ := idx.Encode("catsup")
	sres, _ := finder.FindAllPrefixesOf("catsup")
	bres := finder.FindAllPrefixesOfB(enc)
	if len(sres) != len(bres) {
		t.Fatalf("prefix counts differ: %d vs %d", len(sres), len(bres))
	}
	for i := range sres {
		dec, _ := idx.Decode(bres[i].Word)
		if sres[i].Index != bres[i].Index || sres[i].Word != dec {
			t.Errorf("prefix %d differs: %v vs {%q %d}", i, sres[i], dec, bres[i].Index)
		}
	}

	// Enumerate vs EnumerateB.
	var sEnum, bEnum []string
	finder.Enumerate(func(_ int, w []rune, final bool) int {
		if final {
			sEnum = append(sEnum, string(w))
		}
		return dawg.Continue
	})
	finder.EnumerateB(func(_ int, w []byte, final bool) int {
		if final {
			dec, _ := idx.Decode(w)
			bEnum = append(bEnum, dec)
		}
		return dawg.Continue
	})
	if !slices.Equal(sEnum, bEnum) {
		t.Errorf("Enumerate vs EnumerateB differ: %v vs %v", sEnum, bEnum)
	}
}

// TestIndexOfBOutOfRange verifies that an index outside the alphabet simply
// does not match instead of corrupting the lookup.
func TestIndexOfBOutOfRange(t *testing.T) {
	idx := alphabet.Latin()
	finder := createDawg(t, []string{"cat"})

	if got := finder.IndexOfB([]byte{byte(idx.Size())}); got != -1 {
		t.Errorf("IndexOfB(out-of-range)=%d, want -1", got)
	}
	enc, _ := idx.Encode("cat")
	if got := finder.IndexOfB(enc); got != 0 {
		t.Errorf("IndexOfB(cat)=%d, want 0", got)
	}
}

// TestLoadTruncatedFile verifies Load reports an error (rather than panicking)
// when the file is too short to even contain the size header.
func TestLoadTruncatedFile(t *testing.T) {
	finder := createDawg(t, []string{"cat"})
	path := filepath.Join(t.TempDir(), "trunc.dawg")
	if _, err := finder.Save(path); err != nil {
		t.Fatal(err)
	}

	// Truncate below the 4-byte size header.
	if err := os.Truncate(path, 2); err != nil {
		t.Fatal(err)
	}

	if _, err := dawg.Load(path); err == nil {
		t.Error("Load of a truncated file should return an error, not panic")
	}
}
