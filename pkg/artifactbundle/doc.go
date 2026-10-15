// Package artifactbundle defines the Ready CI artifact archive protocol shared
// by workers, the API, and local artifact-apply tooling.
// Archives allow 100 MiB of file contents and 116 MiB of decompressed tar data
// including headers, metadata, and padding. Producers and consumers enforce both limits.
package artifactbundle
