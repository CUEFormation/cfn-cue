<!-- Title: evaluator: types of absent optional fields are evaluated inside disjunctions; equivalent pattern constraints are ~30x cheaper -->

### What version of CUE are you using (`cue version`)?

<pre>
$ cue version
cue version v0.18.0-alpha.3

CUE language version v0.18.0

Go version go1.27.1
      -buildmode exe
       -compiler gc
       -trimpath true
     CGO_ENABLED 0
          GOARCH arm64
            GOOS darwin
         GOARM64 v8.0
</pre>

Also reproduced with `v0.18.0-alpha.3.0.20261013151513-ffb2adbe9680` (master on 2026-10-13).

### Does this issue reproduce with the latest stable release?

Yes, with v0.17.1 (counts below). v0.17.1 needs `language: version: "v0.17.0"` in `cue.mod/module.cue`.

### What did you do?

A closed struct declares its keys as optional fields with recursive types (`Ref?: string`, `"Fn::If"?: [string, #String, #String]`, …). Properties of a schema are typed `string | #StringExpr`, so that struct is an arm of a disjunction. None of the properties are set.

`optional.cue` and `pattern.cue` are identical except that `pattern.cue` declares the same keys as pattern constraints (`[=~"^Ref$"]: string`). Both accept and reject the same values in my tests. `["Ref"]: string` gives the same counts as the regex form.

Run with [testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/cmd/testscript), or extract with `txtar-x` and run `time cue vet -c optional.cue` and `time cue vet -c pattern.cue`.

```txtar
# optional.cue and pattern.cue differ only in how the keys of #StringExpr,
# #NumberExpr and #ListExpr are declared: Ref?: T versus [=~"^Ref$"]: T.
# Both accept and reject the same values. Compare their time, or their
# Unifications count with CUE_STATS_FILE.
env CUE_STATS_FILE=$WORK/optional-stats.json
exec cue vet -c optional.cue
env CUE_STATS_FILE=$WORK/pattern-stats.json
exec cue vet -c pattern.cue
env CUE_STATS_FILE=

# cue fmt -s rewrites the fast pattern form into the slow optional form.
exec cue fmt -s simplify.cue
cmp simplify.cue simplify.golden

-- cue.mod/module.cue --
module: "example.com/repro@v0"
language: version: "v0.18.0"
-- optional.cue --
package repro

import "list"

#String: string | #StringExpr
#Number: number | #NumberExpr
#List:   [...#String] | #ListExpr

// Function-call objects: exactly one key, each with typed arguments.
#StringExpr: close({
	Ref?:          string
	"Fn::Sub"?:    string | [string, {[string]: #String}]
	"Fn::Join"?:   [string, [...#String] | #ListExpr]
	"Fn::Select"?: [int | #NumberExpr, #List]
	"Fn::If"?:     [string, #String, #String]
})
#NumberExpr: close({
	Ref?:          string
	"Fn::Select"?: [int | #NumberExpr, #List]
	"Fn::If"?:     [string, #Number, #Number]
})
#ListExpr: close({
	Ref?:          string
	"Fn::Split"?:  [string, #String]
	"Fn::Select"?: [int | #NumberExpr, #List]
	"Fn::If"?:     [string, #List, #List]
})

// 40 optional properties, each a literal or a function call. None are set.
#Schema: close({
	for i in list.Range(0, 40, 1) {
		"P\(i)"?: string | #StringExpr
	}
})

for i in list.Range(0, 1000, 1) {
	"x\(i)": #Schema & {}
}
-- pattern.cue --
package repro

import "list"

#String: string | #StringExpr
#Number: number | #NumberExpr
#List:   [...#String] | #ListExpr

// Function-call objects: exactly one key, each with typed arguments.
#StringExpr: close({
	[=~"^Ref$"]:        string
	[=~"^Fn::Sub$"]:    string | [string, {[string]: #String}]
	[=~"^Fn::Join$"]:   [string, [...#String] | #ListExpr]
	[=~"^Fn::Select$"]: [int | #NumberExpr, #List]
	[=~"^Fn::If$"]:     [string, #String, #String]
})
#NumberExpr: close({
	[=~"^Ref$"]:        string
	[=~"^Fn::Select$"]: [int | #NumberExpr, #List]
	[=~"^Fn::If$"]:     [string, #Number, #Number]
})
#ListExpr: close({
	[=~"^Ref$"]:        string
	[=~"^Fn::Split$"]:  [string, #String]
	[=~"^Fn::Select$"]: [int | #NumberExpr, #List]
	[=~"^Fn::If$"]:     [string, #List, #List]
})

// 40 optional properties, each a literal or a function call. None are set.
#Schema: close({
	for i in list.Range(0, 40, 1) {
		"P\(i)"?: string | #StringExpr
	}
})

for i in list.Range(0, 1000, 1) {
	"x\(i)": #Schema & {}
}
-- simplify.cue --
package repro

#F: close({
	["Ref"]: string
})
-- simplify.golden --
package repro

#F: close({
	Ref?: string
})
```

### What did you expect to see?

Similar cost for the two files. `Ref?: T` and `[=~"^Ref$"]: T` describe the same set of values here, and `cue fmt -s` treats `["Ref"]: T` and `Ref?: T` as interchangeable. Since no property is set, I expected the types of absent optional fields not to need evaluating.

### What did you see instead?

The optional-field form does about 29 times as many unifications and takes about 12 times as long (Apple M2, `CUE_STATS_FILE`):

| Version | File | Time | Unifications | Disjuncts | AllocBytes |
|---|---|---|---|---|---|
| v0.18.0-alpha.3 | optional.cue | 5.80 s | 3,607,025 | 880,996 | 2,389 MB |
| v0.18.0-alpha.3 | pattern.cue | 0.48 s | 123,253 | 80,132 | 357 MB |
| master (ffb2adbe9680) | optional.cue | 6.07 s | 3,607,025 | 880,996 | 2,389 MB |
| master (ffb2adbe9680) | pattern.cue | 0.55 s | 123,253 | 80,132 | 357 MB |
| v0.17.1 | optional.cue | 6.28 s | 3,607,026 | 880,992 | 2,220 MB |
| v0.17.1 | pattern.cue | 0.51 s | 123,254 | 80,128 | 334 MB |

The cost appears only when the struct is a disjunct. Typing the properties as `#StringExpr` instead of `string | #StringExpr`, with the same optional keys and empty data, takes 0.16 s and 43,465 unifications. Cost grows linearly with the number of unset properties: about 90 unifications per property per value with optional keys, and about 3 with patterns.

### `cue fmt -s` now produces the slow form

`cue fmt -s` rewrites `["Ref"]: T` into `Ref?: T` (`simplify.cue` → `simplify.golden` above). This is new in v0.18.0-alpha.3, from #1045; v0.17.1 leaves the file unchanged. The two forms are equivalent, so the rewrite is correct, but with the cost difference above it makes a file roughly 12 times slower to evaluate. Users who simplify their files can lose the faster form without noticing. We use regex labels (`[=~"^Ref$"]`), which the formatter leaves alone.

### Context

I hit this generating CUE schemas for AWS CloudFormation resources ([cfn-cue](https://github.com/CUEFormation/cfn-cue)), where every property is `literal | <intrinsic function object>`. Switching the function keys from optional fields to regex pattern constraints, with no other change, cut validation from 11 s to 8 ms per resource in a region package with 6 resource types. With 50 types it went from 20 s per resource (a one-resource template; ten resources didn't finish within 300 s) to 21 ms. Loading the 50-type package went from 14.8 s to 75 ms ([BENCHMARKS.md](https://github.com/CUEFormation/cfn-cue/blob/main/BENCHMARKS.md)).

Possibly related: #2851 (disjunction performance).
