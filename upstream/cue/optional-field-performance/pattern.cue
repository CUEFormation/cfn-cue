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
