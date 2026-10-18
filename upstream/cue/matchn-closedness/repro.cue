package repro

#Embedded: {
 a?: int
 matchN(>=0, [{a!: _}])
}
#Outside: close({a?: int}) & matchN(>=0, [{a!: _}])
#Plain: {a?: int}
embedded: #Embedded & {a: 1, extra: 2}
outside: #Outside & {a: 1, extra: 2}
plain: #Plain & {a: 1, extra: 2}
