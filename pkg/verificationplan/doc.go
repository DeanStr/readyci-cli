// Package verificationplan holds the parts of verification planning that depend
// only on project configuration and a changed-file set — affected-rule matching,
// path patterns, `when` conditions, and matrix expansion.
//
// It exists so the service can derive a plan of its own rather than accept a
// plan compiled by the caller. ADR 0007 records why: authority over a plan is
// meaningless while only the client can say what should have run.
//
// Everything here is pure. Nothing in this package reads the filesystem, runs
// git, or consults the process environment. Producing the inputs — scanning the
// working tree for changed files, loading configuration, reading CI environment
// variables — belongs to the caller, because the CLI and the service obtain them
// from different places and with different levels of trust.
package verificationplan
