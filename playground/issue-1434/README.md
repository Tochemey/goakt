# Issue 1434: MergeSubstreams buffers without bound under a slow downstream

https://github.com/Tochemey/goakt/issues/1434

`MergeSubstreams` collected the output of every substream in one buffer and
emitted from it as its downstream asked. Nothing limited that buffer: the
substreams produced whatever they could, and the pipeline that feeds the
splitter was pulled at full speed. Under a slow downstream the stream held
everything the source produced that it did not drop, and it dropped a large
share of it: the per-substream cap applied the overflow strategy, and
`BackpressureSource` dropped like `DropTail`.

Now a slow downstream holds the source back, whatever the overflow strategy:

- a substream sends at most one demand window ahead of what the downstream has
  taken;
- the splitter stops delivering upstream elements while its merged buffer holds
  a window, and acknowledges an upstream element only once it is delivered.

The overflow strategy only decides what happens to a substream that is slower
than its feed, and only while the downstream asks for elements. `BackpressureSource`, now the default, holds the source back
until that substream has room; `DropTail` and `DropHead` drop its new elements;
`FailSource` fails the stream.

This sample runs a source of 1,000,000 elements through `GroupBy` (four keys)
and `MergeSubstreams` into a sink that blocks on its first element. While the
sink is blocked it counts:

- **pulled**: the elements the source emitted;
- **delivered**: the elements the sink received;
- **dropped**: the elements the stream reported as dropped;
- **held**: pulled - delivered - dropped, the elements sitting inside the
  stream.

It runs two scenarios:

- **default**: the default substream buffer (256, `BackpressureSource`);
- **DropTail**: `WithSubstreamBuffer(256, DropTail)`.

The sample fails when:

- the stream holds more than 8,192 elements, or drops an element, while the
  sink is blocked (both scenarios);
- once the sink is released, the default scenario does not deliver every
  element, or the `DropTail` scenario neither delivers nor counts as dropped an
  element. Running fast, `DropTail` may drop a few elements: the source can
  feed one substream faster than the substream hands them on.

## Run

```
go run ./playground/issue-1434
```

Exit status 0 means every check held, 1 means one did not, and 2 means the
setup failed. The counts vary from run to run.

## Actual (before the fix)

The source runs dry while the sink is blocked, the stream holds every element
it did not drop, and both scenarios drop hundreds of thousands of elements.
Before the fix the default strategy was `DropTail`, and `BackpressureSource`
behaved the same way.

```
default (BackpressureSource): 1000000 elements, 4 substreams, the sink blocks on its first element
  the source ran dry while the sink was blocked
  pulled 1000000, delivered 1, dropped 439028, held 560971 (heap grew by 14.3 MB)
  BUG: the stream holds 560971 elements, more than the limit of 8192
  BUG: the stream dropped 439028 elements while the sink was blocked
  sink released: delivered 560972, dropped 439028
  BUG: the stream lost 439028 of 1000000 elements

DropTail: 1000000 elements, 4 substreams, the sink blocks on its first element
  the source ran dry while the sink was blocked
  pulled 1000000, delivered 1, dropped 412724, held 587275 (heap grew by 16.6 MB)
  BUG: the stream holds 587275 elements, more than the limit of 8192
  BUG: the stream dropped 412724 elements while the sink was blocked
  sink released: delivered 587276, dropped 412724
  OK: every element was delivered or counted as dropped

FAIL: MergeSubstreams did not honor the demand of its downstream
exit status 1
```

## Expected (with the fix)

The source is held back after a few hundred elements and nothing is dropped
while the sink is blocked. Once it is released, the default delivers every
element.

```
default (BackpressureSource): 1000000 elements, 4 substreams, the sink blocks on its first element
  the source was held back: it had not run dry after 3s
  pulled 928, delivered 1, dropped 0, held 927 (heap grew by 0.3 MB)
  OK: the stream holds 927 elements, within the limit of 8192
  sink released: delivered 1000000, dropped 0
  OK: the stream delivered all 1000000 elements

DropTail: 1000000 elements, 4 substreams, the sink blocks on its first element
  the source was held back: it had not run dry after 3s
  pulled 928, delivered 1, dropped 0, held 927 (heap grew by 0.1 MB)
  OK: the stream holds 927 elements, within the limit of 8192
  sink released: delivered 1000000, dropped 0
  OK: every element was delivered or counted as dropped

PASS: under a blocked downstream MergeSubstreams held the source back, held a bounded number of elements and dropped none
```
