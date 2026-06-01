# DAWG of DAFSA with compact alphabet

Package `dafsa` is an implementation of a Directed Acyclic Word Graph (DAWG), a
minimized [DAFSA](https://en.wikipedia.org/wiki/Deterministic_acyclic_finite_state_automaton),
built with the incremental-minimization algorithm described on
[Steve Hanov's blog](http://stevehanov.ca/blog/?id=115). It is designed to be as
memory efficient as possible.

A DAWG provides fast lookup of all the prefixes of a word that are themselves
stored words, as well as the index number of any stored word (and the reverse).

This fork differs from the original in two ways:

- **Compact alphabet.** Characters are stored as
  [`alphabet.Indexer`](https://github.com/iliadenisov/alphabet) indexes (0–62,
  at most six bits) instead of raw runes. This shrinks the graph and binds every
  word to a known alphabet, so ordering and validity are well defined.
- **Index-based API.** Every query has a string form (convenient) and a `*B`
  form that works directly on alphabet indexes, skipping per-call
  encoding/decoding on hot paths.

The storage format packs bits instead of bytes, so no space is wasted as
padding and there is no practical limit on the number of nodes or characters. A
summary of the data format is at the top of `disk.go`.

## Installation

```shell
go get github.com/iliadenisov/dafsa
```

## Usage

Create a builder with `dawg.New(indexer)`, passing an `alphabet.Indexer` that
fixes the alphabet (for example `alphabet.Latin()`). Add words, call `Finish()`,
then query the returned `Finder`.

```go
d := dawg.New(alphabet.Latin())
_ = d.Add("blip")   // index 0
_ = d.Add("cat")    // index 1
_ = d.Add("catnip") // index 2
_ = d.Add("cats")   // index 3

finder := d.Finish()

results, _ := finder.FindAllPrefixesOf("catsup")
for _, r := range results {
    fmt.Printf("%s -> %d\n", r.Word, r.Index) // cat -> 1, cats -> 3
}
```

`IndexOf`, `FindAllPrefixesOf` and `Add` return an error when the input contains
a character outside the alphabet. `IndexOf` returns `(-1, nil)` for a word that
is simply absent.

### Index-based (fast) API

When you already hold alphabet indexes — or you are on a hot path and want to
avoid encoding the query every call — use the `*B` methods. They take and
return `[]byte` indexes and do no encoding or decoding:

```go
word, _ := alphabet.Latin().Encode("cats")
i := finder.IndexOfB(word)             // 3
prefixes := finder.FindAllPrefixesOfB(word)
idxs, _ := finder.AtIndexB(3)          // []byte{2,0,19,18}
finder.EnumerateB(func(index int, w []byte, final bool) int {
    return dawg.Continue
})
```

The builder exposes `AddB` and `CanAddB` for the same reason.

## Alphabet and ordering

Words must be added in strictly increasing order **by alphabet index**, which is
not necessarily Unicode code-point order. For example, in the embedded Russian
alphabet `ё` has index 6 (right after `е`), even though its code point is greater
than `я`. Reusing a word, or adding one out of order, is rejected.

## Persistence

After `Finish()` you may write the DAWG with `Save()` and reopen it later with
`Load()`. When opened from a file the structure is accessed in place, using no
heap memory for the graph itself.

The file stores the alphabet's **language code**, and `Load`/`Read` reconstruct
the alphabet from it via `alphabet.LoadEmbedded`. Consequently only
embedded-language alphabets can be saved and reloaded; `Save` refuses a custom
(non-embedded) alphabet up front, and `Load` returns an error if the stored code
is unknown. Custom alphabets still work fully in memory.

## Performance vs the base (rune-based) version

Measured on the same deterministic 20,000-word lowercase Latin corpus
(`go test -bench . -benchmem -count=3`, Go 1.26). "Base" is the previous
rune-based implementation; "string" and "bytes" are this version's two API forms.

Size and memory:

| Metric                        | Base (rune) | This version |
| ----------------------------- | ----------- | ------------ |
| Serialized file               | 164,997 B   | 152,064 B (**−7.8 %**) |
| Bits per character (`cbits`)  | 7           | 5            |

The per-character saving grows for larger alphabets: Cyrillic drops from 11 to 6
bits per character, for example.

Lookup time and allocations (`ns/op`, `allocs/op`):

| Operation          | Base (rune)      | This (string)    | This (bytes)     |
| ------------------ | ---------------- | ---------------- | ---------------- |
| Build (20k words)  | 37.1 ms / 548k   | 37.0 ms / 536k   | 35.7 ms / 516k   |
| IndexOf            | 349 ns / 1       | 409 ns / 2       | **350 ns / 1**   |
| FindAllPrefixesOf  | 380 ns / 2       | 515 ns / 5       | **380 ns / 2**   |
| AtIndex            | 2159 ns / 24     | 1544 ns / 10     | **1500 ns / 8**  |
| Enumerate (all)    | 5.01 ms / 99.7k  | 4.63 ms / 72.3k  | **4.24 ms / 72.3k** |

Takeaways:

- The `*B` lookups match or beat the base version while storing a smaller
  graph; lookup time is bounded by the bit-level reads (a deliberate design
  trade-off), so the smaller `cbits` helps more for larger alphabets and big,
  cache-bound dictionaries.
- The string methods add the cost of encoding the query through the alphabet;
  prefer the `*B` forms on hot paths.
- `AtIndex` and `Enumerate` are markedly faster and allocate far less, because
  nodes are now decoded into exact-size buffers.

## Comparison with other libraries

The library trades lookup speed for size. Against other Go FSA/trie
implementations ([go-fsa-trie-bench](https://github.com/timurgarif/go-fsa-trie-bench))
it stores the same dictionary in a fraction of the memory (hundreds of KB versus
several MB to tens of MB), at the cost of slower, bit-level lookups.

## Credits

Based on the DAWG implementation and incremental-minimization write-up by
[Steve Hanov](http://stevehanov.ca/blog/?id=115). See `LICENSE`.
