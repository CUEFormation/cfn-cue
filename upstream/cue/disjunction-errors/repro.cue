package repro

#E: close({a!: int}) | close({b!: int})
two: ([...#E] | #E) & [{a: "x"}]
three: ([...#E] | #E | close({c!: int})) & [{a: "x"}]
