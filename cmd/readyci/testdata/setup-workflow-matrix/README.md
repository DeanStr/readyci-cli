# Pinned workflow matrix fixtures

These are unmodified upstream workflow bytes at the original popular-50 audit
pins for Caddy, FastAPI, Gin, and Guava. `provenance.json` records the public
repository, commit, path, immutable source URL, and SHA256 for each file.

The regression tests inspect the `test` job's static axes and `matrix.include`
overlays. The expected concrete variant counts are 3, 15, 20, and 9 respectively.
They preserve the blocked parent matrix and each independent child scope.

These fixtures do not establish that complete workflow imports or repository
tests are supported. The tests also assert that unrelated execution requirements
remain blocked: Caddy's hardening action, FastAPI's uv action, Gin's prerequisite
job, and Guava's Java setup action. Provider state, conditions, native toolchains,
and other prerequisites require separate translation and execution evidence.

The workflows are parsed as data; their commands and actions are never executed
by these regression tests.
