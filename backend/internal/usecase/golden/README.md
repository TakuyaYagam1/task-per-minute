# Golden coordination

This package contains the complete usecase graph for the Golden stage. Each
workflow is grouped by a clear filename prefix and shares one package-level
contract boundary in `ports.go`.

Allowed production dependencies point from the coordinator on the left to the
owned contract on the right:

```text
topology
  <- plan
     <- state
        <- wave
           <- submission

state + wave + submission
  <- attempt

submission + wave
  <- connection

attempt + plan + state + wave
  <- continuation

attempt + plan + state + submission + wave
  <- failure

state + wave
  <- prestart

recovery
```

Canonical ordering and identity helpers are implementation details in
`canonical_*.go`. They do not depend on infrastructure or transport code.

Files may depend on workflows above them in the graph, never on a downstream
coordinator. New graph edges require an ownership review first. Tests and
fixtures stay in this package. Generated mocks stay in `mocks/`.
