package dawg_test

import (
	"encoding/binary"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/iliadenisov/alphabet"
	dawg "github.com/iliadenisov/dafsa"
)

// corpusLatin builds a deterministic, sorted, de-duplicated set of n lowercase
// a-z words. The set depends only on the fixed seed, so it is identical across
// runs and across the before/after migration measurements.
func corpusLatin(n int) []string {
	rng := rand.New(rand.NewSource(42))
	set := make(map[string]struct{}, n*2)
	for len(set) < n {
		l := 3 + rng.Intn(8) // lengths 3..10
		b := make([]byte, l)
		for i := range b {
			b[i] = byte('a' + rng.Intn(26))
		}
		set[string(b)] = struct{}{}
	}
	words := make([]string, 0, len(set))
	for w := range set {
		words = append(words, w)
	}
	sort.Strings(words)
	return words
}

var benchWords = corpusLatin(20000)

var benchWordsB = encodeAll(benchWords)

func encodeAll(words []string) [][]byte {
	idx := alphabet.Latin()
	out := make([][]byte, len(words))
	for i, w := range words {
		b, err := idx.Encode(w)
		if err != nil {
			panic(err)
		}
		out[i] = b
	}
	return out
}

func benchBuild(words []string) dawg.Finder {
	d := dawg.New(alphabet.Latin())
	for _, w := range words {
		d.Add(w)
	}
	return d.Finish()
}

func benchBuildB(words [][]byte) dawg.Finder {
	d := dawg.New(alphabet.Latin())
	for _, w := range words {
		d.AddB(w)
	}
	return d.Finish()
}

func BenchmarkBuild(b *testing.B) {
	words := benchWords
	for b.Loop() {
		benchBuild(words)
	}
}

func BenchmarkIndexOf(b *testing.B) {
	words := benchWords
	f := benchBuild(words)
	i := 0
	for b.Loop() {
		f.IndexOf(words[i%len(words)])
		i++
	}
}

func BenchmarkFindAllPrefixesOf(b *testing.B) {
	words := benchWords
	f := benchBuild(words)
	i := 0
	for b.Loop() {
		f.FindAllPrefixesOf(words[i%len(words)])
		i++
	}
}

func BenchmarkAtIndex(b *testing.B) {
	words := benchWords
	f := benchBuild(words)
	n := f.NumAdded()
	i := 0
	for b.Loop() {
		f.AtIndex(i % n)
		i++
	}
}

func BenchmarkBuildB(b *testing.B) {
	words := benchWordsB
	for b.Loop() {
		benchBuildB(words)
	}
}

func BenchmarkIndexOfB(b *testing.B) {
	f := benchBuild(benchWords)
	words := benchWordsB
	i := 0
	for b.Loop() {
		f.IndexOfB(words[i%len(words)])
		i++
	}
}

func BenchmarkFindAllPrefixesOfB(b *testing.B) {
	f := benchBuild(benchWords)
	words := benchWordsB
	i := 0
	for b.Loop() {
		f.FindAllPrefixesOfB(words[i%len(words)])
		i++
	}
}

func BenchmarkAtIndexB(b *testing.B) {
	f := benchBuild(benchWords)
	n := f.NumAdded()
	i := 0
	for b.Loop() {
		f.AtIndexB(i % n)
		i++
	}
}

func BenchmarkEnumerate(b *testing.B) {
	f := benchBuild(benchWords)
	for b.Loop() {
		f.Enumerate(func(int, []rune, bool) int { return dawg.Continue })
	}
}

func BenchmarkEnumerateB(b *testing.B) {
	f := benchBuild(benchWords)
	for b.Loop() {
		f.EnumerateB(func(int, []byte, bool) int { return dawg.Continue })
	}
}

// TestReportMetrics records graph-size metrics for the current corpus so the
// before/after migration comparison has concrete numbers.
func TestReportMetrics(t *testing.T) {
	words := benchWords
	f := benchBuild(words)

	path := filepath.Join(t.TempDir(), "bench.dawg")
	size, err := f.Save(path)
	if err != nil {
		t.Fatal(err)
	}

	hdr, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fileSize := binary.BigEndian.Uint32(hdr[0:4])
	cbits := hdr[4]
	abits := hdr[5]

	t.Logf("words=%d nodes=%d edges=%d savedBytes=%d fileSizeHdr=%d cbits=%d abits=%d",
		f.NumAdded(), f.NumNodes(), f.NumEdges(), size, fileSize, cbits, abits)
}
