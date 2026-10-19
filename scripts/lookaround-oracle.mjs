#!/usr/bin/env node
// Records how ECMAScript, the regular-expression dialect of JSON Schema,
// evaluates each provider pattern that the generator translates from
// lookarounds into RE2 constraints. The Go tests compare the translations
// with these results; Node is not needed to run them.
//
//   node scripts/lookaround-oracle.mjs
//
// Input:  internal/generate/testdata/lookaround/patterns.json
// Output: internal/generate/testdata/lookaround/oracle.json
//
// Patterns use the u flag, as JSON Schema 2020-12 specifies, unless they are
// only valid without it (for example [0-9-_]). Three non-ECMAScript forms
// that Java, which CloudFormation uses, and RE2 read the same way are
// rewritten first, and the rewrite is recorded: \A becomes ^, an inline (?i)
// becomes a (?i:...) modifier group reaching to the end of its enclosing
// group, and a - after a class escape such as [\p{L}-_] becomes \-.
//
// RE2 and ECMAScript read some escapes differently whether or not a pattern
// has lookarounds: ECMAScript's . also excludes CR, U+2028 and U+2029, and its
// \s includes \v and Unicode spaces. Those differences are outside this
// translation, so samples avoid those characters.
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const dir = join(root, "internal/generate/testdata/lookaround");
const input = JSON.parse(readFileSync(join(dir, "patterns.json"), "utf8"));
const excluded = /[\r\v\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff]/u;

function rewriteInlineFlags(source) {
  for (;;) {
    const at = source.indexOf("(?i)");
    if (at < 0) return source;
    let depth = 0;
    let end = source.length;
    for (let i = at + 4; i < source.length; i++) {
      const c = source[i];
      if (c === "\\") {
        i++;
      } else if (c === "[") {
        for (i++; i < source.length && source[i] !== "]"; i++) {
          if (source[i] === "\\") i++;
        }
      } else if (c === "(") {
        depth++;
      } else if (c === ")") {
        if (depth === 0) {
          end = i;
          break;
        }
        depth--;
      } else if (c === "|" && depth === 0) {
        // The flag would also cover the following alternatives.
        throw new Error(`unsupported inline flag before |: ${source}`);
      }
    }
    source = source.slice(0, at) + "(?i:" + source.slice(at + 4, end) + ")" + source.slice(end);
  }
}

function escapeClassHyphens(source) {
  let out = "";
  let inClass = false;
  for (let i = 0; i < source.length; i++) {
    const c = source[i];
    if (c === "\\") {
      let escape = source.slice(i, i + 2);
      if ("pP".includes(source[i + 1]) && source[i + 2] === "{") {
        escape = source.slice(i, source.indexOf("}", i) + 1);
      }
      out += escape;
      i += escape.length - 1;
      if (inClass && source[i + 1] === "-" && /^\\([dDwWsS]|[pP]\{)/.test(escape)) {
        out += "\\-";
        i++;
      }
    } else {
      if (c === "[") inClass = true;
      if (c === "]") inClass = false;
      out += c;
    }
  }
  return out;
}

function compile(pattern) {
  let source = pattern.replace(/^\\A/, "^");
  source = escapeClassHyphens(rewriteInlineFlags(source));
  for (const flags of ["u", ""]) {
    try {
      return { re: new RegExp(source, flags), source, flags };
    } catch {}
  }
  throw new Error(`not a valid ECMAScript pattern: ${pattern}`);
}

for (const sample of input.samples) {
  if (excluded.test(sample)) throw new Error(`sample uses an excluded character: ${JSON.stringify(sample)}`);
}
const missing = [];
const patterns = input.patterns.map(({ pattern, examples = [] }) => {
  for (const sample of examples) {
    if (excluded.test(sample)) throw new Error(`example uses an excluded character: ${JSON.stringify(sample)}`);
  }
  const { re, source, flags } = compile(pattern);
  const bits = (samples) => samples.map((s) => (re.test(s) ? "1" : "0")).join("");
  const out = { pattern, flags, samples: bits(input.samples) };
  if (source !== pattern) out.ecmascript = source;
  if (examples.length > 0) {
    out.examples = examples;
    out.exampleResults = bits(examples);
  }
  const all = out.samples + (out.exampleResults ?? "");
  if (!all.includes("1") || !all.includes("0")) {
    missing.push(pattern);
  }
  return out;
});
if (missing.length > 0) {
  throw new Error(`patterns need accepted and rejected examples:\n${missing.join("\n")}`);
}
const oracle = { engine: `Node.js ${process.version} RegExp`, samples: input.samples, patterns };
writeFileSync(join(dir, "oracle.json"), JSON.stringify(oracle, null, 2) + "\n");
console.log(`wrote ${patterns.length} patterns x ${input.samples.length} samples`);
