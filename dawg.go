package dawg

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"

	"github.com/iliadenisov/alphabet"
)

// FindResult is the result of a lookup in the d. It
// contains both the word found, and it's index based on the
// order it was added.
type FindResult struct {
	Word  string
	Index int
}

// FindResultB is the index-based counterpart of FindResult returned by
// FindAllPrefixesOfB. Word aliases the query slice passed to that method, so
// copy it if you need to retain it beyond the call.
type FindResultB struct {
	Word  []byte
	Index int
}

type edgeStart struct {
	node int
	ch   byte // alphabet index of the edge character
}

func (edge edgeStart) String() string {
	return fmt.Sprintf("(%d, #%d)", edge.node, edge.ch)
}

type edgeEnd struct {
	node  int
	count int
}

type uncheckedNode struct {
	parent int
	ch     byte
	child  int
}

// EnumFn is a method that you implement. It will be called with
// all prefixes stored in the DAWG. If final is true, the prefix
// represents a complete word that has been stored.
type EnumFn = func(index int, word []rune, final bool) EnumerationResult

// EnumFnB is the index-based counterpart of EnumFn used by EnumerateB.
// word holds alphabet indexes and is reused between calls; copy it if you need
// to retain it.
type EnumFnB = func(index int, word []byte, final bool) EnumerationResult

// EnumerationResult is returned by the enumeration function to indicate whether
// indication should continue below this depth or to stop altogether
type EnumerationResult = int

const (
	// Continue enumerating all words with this prefix
	Continue EnumerationResult = iota

	// Skip will skip all words with this prefix
	Skip

	// Stop will immediately stop enumerating words
	Stop
)

// Finder is the interface for querying a dawg. Use either
// Builder.Finish() or Load() to obtain one.
//
// Each method comes in two forms: a string form that encodes/decodes through the
// alphabet (convenient), and a *B form that works directly on alphabet
// indexes (fast, no per-call encoding). Use the *B form on hot paths when
// you already hold indexes.
type Finder interface {
	// FindAllPrefixesOf finds all stored words that are a prefix of input. It
	// returns an error if input contains a character outside the alphabet.
	FindAllPrefixesOf(input string) ([]FindResult, error)

	// FindAllPrefixesOfB is the index-based form of FindAllPrefixesOf.
	FindAllPrefixesOfB(word []byte) []FindResultB

	// IndexOf returns the insertion index of input, or (-1, nil) if it was never
	// added. It returns an error if input contains a character outside the
	// alphabet.
	IndexOf(input string) (int, error)

	// IndexOfB is the index-based form of IndexOf. It returns -1 if the word
	// was never added.
	IndexOfB(word []byte) int

	// AtIndex returns the word stored at the given insertion index.
	AtIndex(index int) (string, error)

	// AtIndexB is the index-based form of AtIndex, returning alphabet
	// indexes.
	AtIndexB(index int) ([]byte, error)

	// Enumerate all prefixes stored in the dawg.
	Enumerate(fn EnumFn)

	// EnumerateB is the index-based form of Enumerate.
	EnumerateB(fn EnumFnB)

	// Returns the number of words
	NumAdded() int

	// Returns the number of edges
	NumEdges() int

	// Returns the number of nodes
	NumNodes() int

	// Output a human-readable description of the dawg to stdout
	Print()

	// Close the dawg that was opened with Load(). After this, it is no longer
	// accessible.
	Close() error

	// Save to a writer
	Write(w io.Writer) (int64, error)

	// Save to a file
	Save(filename string) (int64, error)
}

// Builder is the interface for creating a new Dawg. Use New() to create it.
type Builder interface {
	// Add the word to the dawg. It returns an error if the dawg is finished,
	// the word contains a character outside the alphabet, or the word does not
	// order strictly after the previously added word by alphabet index.
	Add(word string) error

	// AddB is the index-based form of Add, taking alphabet indexes directly.
	AddB(word []byte) error

	// CanAdd reports whether Add(word) would succeed.
	CanAdd(word string) bool

	// CanAddB reports whether AddB(word) would succeed.
	CanAddB(word []byte) bool

	// Complete the dawg and return a Finder.
	Finish() Finder
}

const rootNode = 0

type node struct {
	final bool
	count int
	edges []edgeStart
}

// dawg represents a Directed Acyclic Word Graph
type dawg struct {
	// indexer maps alphabet characters to compact byte indexes and back. It
	// constrains which characters may be added or searched and defines the
	// ordering used throughout the graph.
	indexer alphabet.Indexer

	// decoder maps an alphabet index back to its rune in O(1). It is built for
	// finders (after Finish or Load) and is nil on a builder.
	decoder []rune

	// these are erased after we finish building
	lastWord       []byte
	nextID         int
	uncheckedNodes []uncheckedNode
	minimizedNodes map[string]int
	nodes          map[int]*node

	// if read from a file, this is set
	r    io.ReaderAt
	size int64 // size of the readerAt

	// these are kept
	finished        bool
	numAdded        int
	numNodes        int
	numEdges        int
	cbits           int64 // bits to represent character value
	abits           int64 // bits to represent node address
	wbits           int64 // bits to represent number of words / counts
	firstNodeOffset int64 // first node offset in bits in the file
	hasEmptyWord    bool
}

// New creates a new dawg Builder that uses indexer to encode and order the
// words it is given. Every word added to the builder, and later searched in the
// resulting Finder, must consist of characters from indexer's alphabet.
func New(indexer alphabet.Indexer) Builder {
	return &dawg{
		indexer:        indexer,
		nextID:         1,
		minimizedNodes: make(map[string]int),
		nodes: map[int]*node{
			0: {count: -1},
		},
	}
}

// CanAdd reports whether word can be added: the dawg must be unfinished, word
// must encode in the alphabet, and it must order strictly after the previously
// added word by alphabet index.
func (d *dawg) CanAdd(word string) bool {
	if d.finished {
		return false
	}
	idx, err := d.indexer.Encode(word)
	if err != nil {
		return false
	}
	return d.CanAddB(idx)
}

// CanAddB is the index-based form of CanAdd.
func (d *dawg) CanAddB(word []byte) bool {
	if d.finished {
		return false
	}
	if !d.indexesInRange(word) {
		return false
	}
	return d.numAdded == 0 || bytes.Compare(word, d.lastWord) > 0
}

// Add adds a word to the structure. It returns an error if the dawg is already
// finished, if word contains a character outside the alphabet, or if word does
// not order strictly after the previously added word by alphabet index.
func (d *dawg) Add(wordIn string) error {
	word, err := d.indexer.Encode(wordIn)
	if err != nil {
		return fmt.Errorf("dawg: cannot add %q: %w", wordIn, err)
	}
	return d.AddB(word)
}

// AddB adds a word given directly as alphabet indexes. The same ordering and
// uniqueness rules as Add apply. word must not be modified until the next
// AddB call or Finish, as the builder keeps it as the previous word.
func (d *dawg) AddB(word []byte) error {
	if d.finished {
		return errors.New("dawg: Add called on a finished dawg")
	}
	if !d.indexesInRange(word) {
		return fmt.Errorf("dawg: word contains an index outside the alphabet of size %d", d.indexer.Size())
	}

	if d.numAdded > 0 && bytes.Compare(word, d.lastWord) <= 0 {
		last, _ := d.indexer.Decode(d.lastWord)
		now, _ := d.indexer.Decode(word)
		return fmt.Errorf("dawg: words must be added in increasing alphabet order: last=%q new=%q", last, now)
	}

	// find common prefix between word and previous word
	commonPrefix := 0
	for i := 0; i < min(len(word), len(d.lastWord)); i++ {
		if word[i] != d.lastWord[i] {
			break
		}
		commonPrefix++
	}

	// Check the uncheckedNodes for redundant nodes, proceeding from last
	// one down to the common prefix size. Then truncate the list at that
	// point.
	d.minimize(commonPrefix)

	// add the suffix, starting from the correct node mid-way through the
	// graph
	var node int
	if len(d.uncheckedNodes) == 0 {
		node = rootNode
	} else {
		node = d.uncheckedNodes[len(d.uncheckedNodes)-1].child
	}

	for _, letter := range word[commonPrefix:] {
		nextNode := d.newNode()
		d.addChild(node, letter, nextNode)
		d.uncheckedNodes = append(d.uncheckedNodes, uncheckedNode{node, letter, nextNode})
		node = nextNode
	}

	d.setFinal(node)
	d.lastWord = word
	d.numAdded++
	return nil
}

// indexesInRange reports whether every index in word is a valid alphabet index.
func (d *dawg) indexesInRange(word []byte) bool {
	size := d.indexer.Size()
	for _, ix := range word {
		if int(ix) >= size {
			return false
		}
	}
	return true
}

// Finish will mark the dawg as complete. The dawg cannot be used for lookups
// until Finish has been called.
func (d *dawg) Finish() Finder {
	if !d.finished {
		d.finished = true

		d.minimize(0)

		d.numNodes = len(d.minimizedNodes) + 1

		// Fill in the counts
		d.calculateSkipped(rootNode)

		// no longer need the names.
		d.uncheckedNodes = nil
		d.minimizedNodes = nil
		d.lastWord = nil

		d.renumber()

		var buffer bytes.Buffer
		size, err := d.Write(&buffer)
		if err != nil {
			// Writing to an in-memory buffer cannot fail for a finished dawg;
			// a failure here is an internal invariant violation.
			panic(fmt.Errorf("dawg: internal error serializing the dawg: %w", err))
		}
		d.size = size
		d.r = bytes.NewReader(buffer.Bytes())
		d.nodes = nil
	}

	// Carry the live indexer into the finder directly. This keeps custom
	// (non-embedded) alphabets usable in memory, since Read can only
	// reconstruct embedded alphabets from the stored language code.
	finder, err := read(d.r, 0, d.indexer)
	if err != nil {
		panic(fmt.Errorf("dawg: internal error reopening the dawg: %w", err))
	}

	return finder
}

func (d *dawg) renumber() {
	// after minimization, nodes have been removed so there are gaps in the node IDs.
	// Renumber them all to be consecutive.
	// process them in a depth-first order so that runs of characters
	// will appear in consecutive nodes, which is more efficient for encoding.

	remap := make(map[int]int)

	var process func(id int)

	process = func(id int) {
		if _, ok := remap[id]; ok {
			return
		}

		remap[id] = len(remap)
		node := d.nodes[id]
		for _, edge := range node.edges {
			process(edge.node)
		}
	}

	process(rootNode)

	nodes := make(map[int]*node)
	for id, node := range d.nodes {
		nodes[remap[id]] = node
		for i := range node.edges {
			node.edges[i].node = remap[node.edges[i].node]
		}
	}
	d.nodes = nodes
}

// Print will print all edges to the standard output
func (d *dawg) Print() {
	DumpFile(d.r)
}

// FindAllPrefixesOf returns all items in the dawg that are a prefix of the input string.
// It will panic if the dawg is not finished.
func (d *dawg) FindAllPrefixesOf(input string) ([]FindResult, error) {
	d.checkFinished()

	word, err := d.indexer.Encode(input)
	if err != nil {
		return nil, fmt.Errorf("dawg: cannot search %q: %w", input, err)
	}

	byteResults := d.FindAllPrefixesOfB(word)
	if len(byteResults) == 0 {
		return nil, nil
	}

	results := make([]FindResult, len(byteResults))
	for i, br := range byteResults {
		w, derr := d.decode(br.Word)
		if derr != nil {
			return nil, derr
		}
		results[i] = FindResult{Word: w, Index: br.Index}
	}
	return results, nil
}

// FindAllPrefixesOfB is the index-based form of FindAllPrefixesOf. Each
// result's Word aliases a prefix of word.
func (d *dawg) FindAllPrefixesOfB(word []byte) []FindResultB {
	var results []FindResultB
	skipped := 0
	final := d.hasEmptyWord
	node := rootNode
	var ee edgeEnd
	var ok bool

	r := newBitSeeker(d.r)

	// for each character of the input
	for pos, ix := range word {
		// if the node is final, add a result
		if final {
			results = append(results, FindResultB{Word: word[:pos], Index: skipped})
		}

		// check if there is an outgoing edge for the letter
		ee, final, ok = d.getEdge(&r, edgeStart{node: node, ch: ix})
		if !ok {
			return results
		}

		// we found an edge.
		node = ee.node
		skipped += ee.count
	}

	if final {
		results = append(results, FindResultB{Word: word, Index: skipped})
	}

	return results
}

// IndexOf returns the index, which is the order the item was inserted.
// If the item was never inserted, it returns (-1, nil). It returns an error if
// input contains a character outside the alphabet.
func (d *dawg) IndexOf(input string) (int, error) {
	word, err := d.indexer.Encode(input)
	if err != nil {
		return -1, fmt.Errorf("dawg: cannot look up %q: %w", input, err)
	}
	return d.IndexOfB(word), nil
}

// IndexOfB is the index-based form of IndexOf. An index outside the alphabet
// simply will not match, yielding -1.
func (d *dawg) IndexOfB(word []byte) int {
	skipped := 0
	node := rootNode
	final := d.hasEmptyWord
	var ok bool
	var ee edgeEnd
	r := newBitSeeker(d.r)

	for _, ix := range word {
		ee, final, ok = d.getEdge(&r, edgeStart{node: node, ch: ix})
		if !ok {
			// not found
			return -1
		}

		// we found an edge.
		node = ee.node
		skipped += ee.count
	}

	if final {
		return skipped
	}
	return -1
}

// NumAdded returns the number of words added
func (d *dawg) NumAdded() int {
	return d.numAdded
}

// NumNodes returns the number of nodes in the d.
func (d *dawg) NumNodes() int {
	return d.numNodes
}

// NumEdges returns the number of edges in the d. This includes transitions to
// the "final" node after each word.
func (d *dawg) NumEdges() int {
	return d.numEdges
}

func (d *dawg) checkFinished() {
	if !d.finished {
		panic(errors.New("dawg was not Finished()"))
	}
}

// buildDecoder fills d.decoder so that an alphabet index maps to its rune in
// O(1). Alphabet indexes are contiguous in the range [0, Size).
func (d *dawg) buildDecoder() {
	size := d.indexer.Size()
	d.decoder = make([]rune, size)
	for i := range size {
		s, err := d.indexer.Character(byte(i))
		if err != nil {
			continue
		}
		if rs := []rune(s); len(rs) > 0 {
			d.decoder[i] = rs[0]
		}
	}
}

// decode turns alphabet indexes back into a string using the decoder table.
func (d *dawg) decode(indexes []byte) (string, error) {
	runes := make([]rune, len(indexes))
	for i, idx := range indexes {
		if int(idx) >= len(d.decoder) {
			return "", fmt.Errorf("dawg: alphabet index %d out of range", idx)
		}
		runes[i] = d.decoder[idx]
	}
	return string(runes), nil
}

func (d *dawg) minimize(downTo int) {
	// proceed from the leaf up to a certain point
	for i := len(d.uncheckedNodes) - 1; i >= downTo; i-- {
		u := d.uncheckedNodes[i]
		name := d.nameOf(u.child)
		if node, ok := d.minimizedNodes[name]; ok {
			// replace the child with the previously encountered one
			d.replaceChild(u.parent, u.ch, node)
		} else {
			// add the state to the minimized nodes.
			d.minimizedNodes[name] = u.child
		}
	}

	d.uncheckedNodes = d.uncheckedNodes[:downTo]
}

func (d *dawg) newNode() int {
	d.nextID++
	return d.nextID - 1
}

func (d *dawg) nameOf(nodeid int) string {
	node := d.nodes[nodeid]

	// node name is id_ch:id... for each child
	buff := bytes.Buffer{}
	for _, edge := range node.edges {
		buff.WriteByte('_')
		buff.WriteByte(edge.ch)
		buff.WriteByte(':')
		buff.WriteString(strconv.Itoa(edge.node))
	}

	if node.final {
		buff.WriteByte('!')
	}

	return buff.String()
}

func (d *dawg) setFinal(node int) {
	d.nodes[node].final = true
	if node == rootNode {
		d.hasEmptyWord = true
	}
}

func (d *dawg) addChild(parent int, ch byte, child int) {
	d.numEdges++
	if d.nodes[child] == nil {
		d.nodes[child] = &node{
			count: -1,
		}
	}
	node := d.nodes[parent]
	if len(node.edges) > 0 && ch <= node.edges[len(node.edges)-1].ch {
		log.Panic("Not strictly increasing")
	}
	node.edges = append(node.edges, edgeStart{child, ch})
}

func (d *dawg) replaceChild(parent int, ch byte, child int) {
	pnode := d.nodes[parent]

	i := bsearch(len(pnode.edges), func(i int) int {
		return int(pnode.edges[i].ch) - int(ch)
	})

	if pnode.edges[i].ch != ch {
		log.Panicf("Not found: #%d", ch)
	}

	delete(d.nodes, pnode.edges[i].node)
	pnode.edges[i].node = child

}

func (d *dawg) calculateSkipped(nodeid int) int {
	// for each child of the node, calculate now many nodes
	// are skipped over by following that child. This is the
	// sum of all skipped-over counts of its previous siblings.

	// returns the number of leaves reachable from the node.
	node := d.nodes[nodeid]
	if node.count >= 0 {
		return node.count
	}

	numReachable := 0

	if node.final {
		numReachable++
	}

	for _, edge := range node.edges {
		numReachable += d.calculateSkipped(edge.node)
	}

	node.count = numReachable

	return numReachable
}

// Enumerate will call the given method, passing it every possible prefix of words in the index.
// Return Continue to continue enumeration, Skip to skip this branch, or Stop to stop enumeration.
func (d *dawg) Enumerate(fn EnumFn) {
	// reuse one rune buffer across calls; the decoded prefix is rebuilt from the
	// byte prefix each call.
	var runes []rune
	d.EnumerateB(func(index int, word []byte, final bool) EnumerationResult {
		runes = runes[:0]
		for _, b := range word {
			runes = append(runes, d.decoder[b])
		}
		return fn(index, runes, final)
	})
}

// EnumerateB is the index-based form of Enumerate.
func (d *dawg) EnumerateB(fn EnumFnB) {
	r := newBitSeeker(d.r)
	d.enumerate(&r, 0, rootNode, nil, fn)
}

func (d *dawg) enumerate(r *bitSeeker, index int, address int, word []byte, fn EnumFnB) EnumerationResult {
	// get the node and whether its final
	node := d.getNode(r, address)

	// call the enum function on the prefix
	result := fn(index, word, node.final)

	// if the function didn't say to continue, then return.
	if result != Continue {
		return result
	}

	l := len(word)
	word = append(word, 0)

	// for each edge
	for _, edge := range node.edges {
		// add the index to the prefix
		word[l] = edge.ch
		// recurse
		result = d.enumerate(r, index+edge.count, edge.node, word, fn)
		if result == Stop {
			break
		}
	}

	return result
}

func (d *dawg) AtIndex(index int) (string, error) {
	word, err := d.AtIndexB(index)
	if err != nil {
		return "", err
	}
	return d.decode(word)
}

// AtIndexB is the index-based form of AtIndex, returning alphabet indexes.
func (d *dawg) AtIndexB(index int) ([]byte, error) {
	if index < 0 || index >= d.NumAdded() {
		return nil, errors.New("invalid index")
	}

	r := newBitSeeker(d.r)
	// start at first node and empty word
	result, _ := d.atIndex(&r, rootNode, 0, index, nil)
	return result, nil
}

func (d *dawg) atIndex(r *bitSeeker, nodeNumber, atIndex, targetIndex int, indexes []byte) ([]byte, bool) {
	node := d.getNode(r, nodeNumber)
	// if node is final and index matches, return it
	if node.final && atIndex == targetIndex {
		return indexes, true
	}

	next := bsearch(len(node.edges), func(i int) int {
		return atIndex + node.edges[i].count - targetIndex
	})

	if next == len(node.edges) || atIndex+node.edges[next].count > targetIndex {
		next--
	}

	indexes = append(indexes, 0)
	for i := next; i < len(node.edges); i++ {
		indexes[len(indexes)-1] = node.edges[i].ch
		if result, ok := d.atIndex(r, node.edges[i].node, atIndex+node.edges[i].count, targetIndex, indexes); ok {
			return result, ok
		}
	}
	return nil, false

}
