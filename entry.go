package main

import "stilday/internal/artifact"

// Entry is the artifact package's event type. The UI reads and renders it;
// its JSON shape and the year files are internal/artifact's business.
type Entry = artifact.Entry
