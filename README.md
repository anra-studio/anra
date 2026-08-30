# Anra

Anra is a library-only, single-principal agent harness built above tape-go.

V0 is deliberately limited to the append-only lifecycle, projection, and
recoverable agent-loop foundations. Adapters, daemons, CLIs, network services,
and provider-specific runtime types are out of scope.

The lifecycle and Action vocabulary lives in `pkg/`. The tape-go Runner façade
is a prerequisite owned by tape-go; Anra will consume that façade without
importing its underlying runtime.
