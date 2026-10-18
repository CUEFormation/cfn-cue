package repro

import "encoding/json"
#S: close({A?: string, B?: string}) & matchN(1, [{A!: _}, {B!: _}])
#P: close({s?: #S})
direct: #P & {s: {}}
viaJSON: "{\"s\":{}}" & json.Validate(#P)
both: "{\"s\":{\"A\":\"a\",\"B\":\"b\"}}" & json.Validate(#P)
