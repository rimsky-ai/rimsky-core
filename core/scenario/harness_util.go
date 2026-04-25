package scenario

import (
	"bytes"
	"io"

	"github.com/google/uuid"
)

// bytesReader wraps a byte slice as an io.Reader for http.Post bodies.
func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// parseUUIDStr is a thin shim around uuid.Parse kept alongside the harness
// for readability in DeployTemplate / CreateInstance.
func parseUUIDStr(s string) (uuid.UUID, error) { return uuid.Parse(s) }
